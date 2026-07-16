/**
 * relativeTime formats an ISO timestamp as a human-friendly relative time string.
 * Examples: "just now", "3m ago", "2h ago", "5d ago".
 */
export function relativeTime(isoString: string | null | undefined): string {
  if (!isoString) return '—';
  const then = new Date(isoString).getTime();
  if (Number.isNaN(then)) return '—';
  const now = Date.now();
  const diffMs = now - then;
  if (diffMs < 0) return 'just now';
  const secs = Math.floor(diffMs / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) return `${hrs}h ago`;
  const days = Math.floor(hrs / 24);
  if (days < 30) return `${days}d ago`;
  const months = Math.floor(days / 30);
  return `${months}mo ago`;
}
