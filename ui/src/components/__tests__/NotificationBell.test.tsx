import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { NotificationBell } from '@/components/NotificationBell';

// connhealth-2: the bell renders connection-health alerts (NotificationType
// `connection`, written by the Story 1 backend poller) and, when clicked,
// navigates to Settings → Connection.

const mockNavigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>(
    'react-router-dom',
  );
  return {
    ...actual,
    useNavigate: () => mockNavigate,
  };
});

const getNotifications = vi.fn();
const markAllNotificationsRead = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getNotifications: () => getNotifications(),
    markAllNotificationsRead: () => markAllNotificationsRead(),
  },
}));

function renderBell() {
  return render(
    <MemoryRouter>
      <NotificationBell />
    </MemoryRouter>,
  );
}

const connectionNotification = {
  id: 'conn-1',
  type: 'connection',
  title: "Sharko can't reach your Git connection",
  description: 'The Git connection used for commits and PRs is unreachable.',
  timestamp: new Date().toISOString(),
  read: false,
};

describe('NotificationBell — connection-health alerts', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    markAllNotificationsRead.mockResolvedValue({});
  });

  it('renders a connection-type notification with its icon and title', async () => {
    getNotifications.mockResolvedValue({ notifications: [connectionNotification] });
    renderBell();

    // Open the dropdown.
    fireEvent.click(screen.getByLabelText('Notifications'));

    await waitFor(() => {
      expect(
        screen.getByText("Sharko can't reach your Git connection"),
      ).toBeInTheDocument();
    });

    // The connection-type icon (🔌) is rendered for the item.
    expect(screen.getByText('🔌')).toBeInTheDocument();
  });

  it('navigates to Settings → Connection when a connection alert is clicked', async () => {
    getNotifications.mockResolvedValue({ notifications: [connectionNotification] });
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));

    const item = await screen.findByText(
      "Sharko can't reach your Git connection",
    );
    // Click the actionable item (the role=button wrapper).
    fireEvent.click(item);

    expect(mockNavigate).toHaveBeenCalledWith('/settings?section=connections');
  });

  it('does not navigate for non-connection notifications', async () => {
    getNotifications.mockResolvedValue({
      notifications: [
        {
          id: 'up-1',
          type: 'upgrade',
          title: 'New addon version available',
          description: 'argo-cd 2.10.0 is available.',
          timestamp: new Date().toISOString(),
          read: false,
        },
      ],
    });
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));

    const item = await screen.findByText('New addon version available');
    fireEvent.click(item);

    expect(mockNavigate).not.toHaveBeenCalled();
  });
});

// V2-cleanup-61.4 (G2): the dropdown used to be a hand-rolled `absolute` div
// with a manual `mousedown` outside-click listener — no Escape handling, no
// focus trap, no ARIA. It's now the shadcn/Radix Popover primitive.
describe('NotificationBell — dropdown keyboard behavior (V2-cleanup-61.4, G2)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    getNotifications.mockResolvedValue({ notifications: [] });
  });

  it('closes on Escape', async () => {
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));
    await waitFor(() => {
      expect(screen.getByText('No notifications')).toBeInTheDocument();
    });

    fireEvent.keyDown(screen.getByText('No notifications'), { key: 'Escape' });

    await waitFor(() => {
      expect(screen.queryByText('No notifications')).not.toBeInTheDocument();
    });
  });

  it('closes on outside click', async () => {
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));
    await waitFor(() => {
      expect(screen.getByText('No notifications')).toBeInTheDocument();
    });

    fireEvent.pointerDown(document.body);

    await waitFor(() => {
      expect(screen.queryByText('No notifications')).not.toBeInTheDocument();
    });
  });
});
