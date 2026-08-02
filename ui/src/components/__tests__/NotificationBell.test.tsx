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
const markNotificationRead = vi.fn();
vi.mock('@/services/api', () => ({
  api: {
    getNotifications: () => getNotifications(),
    markAllNotificationsRead: () => markAllNotificationsRead(),
    markNotificationRead: (id: string) => markNotificationRead(id),
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
    markNotificationRead.mockResolvedValue({});
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

  it('navigates to Settings → Connection AND marks it read when a connection alert is clicked (S3)', async () => {
    getNotifications.mockResolvedValue({ notifications: [connectionNotification] });
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));

    const item = await screen.findByText(
      "Sharko can't reach your Git connection",
    );
    // Click the actionable item (the role=button wrapper).
    fireEvent.click(item);

    expect(mockNavigate).toHaveBeenCalledWith('/settings?section=connections');
    expect(markNotificationRead).toHaveBeenCalledWith('conn-1');
  });

  it('does not navigate for non-connection notifications, but still marks them read (S3)', async () => {
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
    expect(markNotificationRead).toHaveBeenCalledWith('up-1');
  });
});

// S3 (walk day 4): every notification item is now clickable and marks
// itself read on click, in addition to any type-specific navigation.
describe('NotificationBell — click marks a single item read (S3)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    markAllNotificationsRead.mockResolvedValue({});
    markNotificationRead.mockResolvedValue({});
  });

  it('clears the unread dot on the clicked item only, and calls the mark-read endpoint', async () => {
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
        {
          id: 'drift-1',
          type: 'drift',
          title: 'Version drift detected',
          description: 'prometheus differs across clusters.',
          timestamp: new Date().toISOString(),
          read: false,
        },
      ],
    });
    renderBell();

    fireEvent.click(screen.getByLabelText('Notifications'));
    const item = await screen.findByText('New addon version available');
    expect(item.closest('[role="button"]')).not.toBeNull();

    fireEvent.click(item);

    await waitFor(() => {
      expect(markNotificationRead).toHaveBeenCalledWith('up-1');
    });

    // The unread count badge drops from 2 to 1 (optimistic local update).
    await waitFor(() => {
      expect(screen.getByText(/Notifications \(1\)/)).toBeInTheDocument();
    });
  });

  it('"Mark all as read" still clears every item', async () => {
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
    await screen.findByText('New addon version available');

    fireEvent.click(screen.getByText('Mark all as read'));

    await waitFor(() => {
      expect(markAllNotificationsRead).toHaveBeenCalled();
    });
    await waitFor(() => {
      expect(screen.getByText('Notifications')).toBeInTheDocument();
      expect(screen.queryByText(/Notifications \(/)).not.toBeInTheDocument();
    });
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
