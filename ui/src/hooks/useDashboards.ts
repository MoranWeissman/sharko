import { useState, useCallback, useEffect } from 'react';
import { api } from '@/services/api';

export interface EmbeddedDashboard {
  id: string;
  name: string;
  url: string;
  provider: 'datadog' | 'grafana' | 'custom';
}

/** Extract the src URL from an iframe snippet, or return the input as-is. */
export function extractUrlFromIframe(input: string): string {
  const match = input.match(/src=["']([^"']+)["']/);
  return match ? match[1] : input;
}

export function useDashboards() {
  const [dashboards, setDashboards] = useState<EmbeddedDashboard[]>([]);
  const [loaded, setLoaded] = useState(false);

  // Load from backend on mount
  useEffect(() => {
    api.getEmbeddedDashboards()
      .then((data) => {
        setDashboards((data ?? []) as EmbeddedDashboard[]);
        setLoaded(true);
      })
      .catch(() => {
        // Fallback: try localStorage for backwards compat
        try {
          const raw = localStorage.getItem('aap-dashboards');
          if (raw) setDashboards(JSON.parse(raw));
        } catch { /* ignore */ }
        setLoaded(true);
      });
  }, []);

  const persist = useCallback(async (next: EmbeddedDashboard[]) => {
    setDashboards(next);
    try {
      await api.saveEmbeddedDashboards(next);
    } catch {
      // Fallback: save to localStorage
      localStorage.setItem('aap-dashboards', JSON.stringify(next));
    }
  }, []);

  const addDashboard = useCallback(
    (dashboard: Omit<EmbeddedDashboard, 'id'>) => {
      const newDashboard: EmbeddedDashboard = {
        ...dashboard,
        id: crypto.randomUUID?.() ?? Date.now().toString(),
      };
      const next = [...dashboards, newDashboard];
      void persist(next);
    },
    [dashboards, persist],
  );

  const updateDashboard = useCallback(
    (id: string, updates: Partial<Omit<EmbeddedDashboard, 'id'>>) => {
      const next = dashboards.map((d) => (d.id === id ? { ...d, ...updates } : d));
      void persist(next);
    },
    [dashboards, persist],
  );

  const removeDashboard = useCallback(
    (id: string) => {
      const next = dashboards.filter((d) => d.id !== id);
      void persist(next);
    },
    [dashboards, persist],
  );

  return { dashboards, loaded, addDashboard, updateDashboard, removeDashboard };
}
