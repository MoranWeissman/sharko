/**
 * Cosmetic "type pill" derived from a cluster's API server hostname at
 * render time. Never persisted. Purely decorative — connectivity testing
 * routes off the ArgoCD cluster Secret shape, not this badge, so a
 * misidentification (e.g. an EKS cluster behind custom DNS rendering as
 * `Self-hosted`) is acceptable.
 *
 * Hostname mapping:
 *
 * | Pattern                                   | Pill          | Tone          |
 * |-------------------------------------------|---------------|---------------|
 * | `*.eks.amazonaws.com`                     | `EKS`         | orange        |
 * | `*.azmk8s.io`                             | `AKS`         | sky blue      |
 * | `*.gke.io` OR `*.googleapis.com`          | `GKE`         | red           |
 * | `kind-*` OR `localhost` OR `127.0.0.1`    | `kind`        | neutral       |
 * | `*.minikube.io`                           | `minikube`    | neutral       |
 * | anything else (incl. malformed / empty)   | `Self-hosted` | neutral       |
 *
 * Empty / malformed input still renders `Self-hosted` so the column is never
 * visually empty. `new URL()` failures are caught — we never throw.
 */

type ClusterType = 'EKS' | 'AKS' | 'GKE' | 'kind' | 'minikube' | 'Self-hosted'

type ClusterTypeBadgeProps = {
  /** The cluster's API server URL, e.g. `"https://kind-test-1:6443"`.
   *  Optional / empty / malformed inputs all fall through to `Self-hosted`. */
  server: string | undefined
  /** Smaller chip for table rows / cards; default sizing for headers. */
  compact?: boolean
}

/**
 * Extracts the lower-cased hostname from a server URL. Returns the empty
 * string for malformed inputs so the caller can fall through to the
 * Self-hosted default without a try/catch. The browser `URL` constructor
 * already strips port, path, query, fragment.
 */
function extractHostname(server: string | undefined): string {
  if (!server || server.trim() === '') return ''
  try {
    return new URL(server).hostname.toLowerCase()
  } catch {
    return ''
  }
}

/**
 * Maps a hostname to a `ClusterType`. Order matters: more-specific
 * cloud-provider patterns are checked first, then dev-flavour heuristics,
 * then fall through to `Self-hosted`.
 */
export function classifyClusterType(server: string | undefined): ClusterType {
  const host = extractHostname(server)
  if (host === '') return 'Self-hosted'

  if (host.endsWith('.eks.amazonaws.com')) return 'EKS'
  if (host.endsWith('.azmk8s.io')) return 'AKS'
  if (host.endsWith('.gke.io') || host.endsWith('.googleapis.com')) return 'GKE'
  if (host.endsWith('.minikube.io')) return 'minikube'
  if (host.startsWith('kind-') || host === 'localhost' || host === '127.0.0.1') {
    return 'kind'
  }

  return 'Self-hosted'
}

type ToneClasses = { base: string; ring: string }

/**
 * Per-type colour tokens. Cloud-provider pills use Tailwind palette utilities
 * (these are NOT gray and the brand-association is the point). Neutral pills
 * follow the Sharko blue-tinted hex family.
 */
const TYPE_TONE: Record<ClusterType, ToneClasses> = {
  EKS: {
    base:
      'bg-orange-100 text-orange-800 dark:bg-orange-900/30 dark:text-orange-300',
    ring: 'ring-orange-300/70 dark:ring-orange-700/70',
  },
  AKS: {
    base: 'bg-sky-100 text-sky-800 dark:bg-sky-900/30 dark:text-sky-300',
    ring: 'ring-sky-300/70 dark:ring-sky-700/70',
  },
  GKE: {
    base: 'bg-red-100 text-red-800 dark:bg-red-900/30 dark:text-red-300',
    ring: 'ring-red-300/70 dark:ring-red-700/70',
  },
  kind: {
    base: 'bg-[#eaf4fc] text-[#2a5a7a] dark:bg-[#123044] dark:text-[#b4dcf5]',
    ring: 'ring-[#c0ddf0] dark:ring-[#2a5a7a]',
  },
  minikube: {
    base: 'bg-[#eaf4fc] text-[#2a5a7a] dark:bg-[#123044] dark:text-[#b4dcf5]',
    ring: 'ring-[#c0ddf0] dark:ring-[#2a5a7a]',
  },
  'Self-hosted': {
    base: 'bg-[#eaf4fc] text-[#2a5a7a] dark:bg-[#123044] dark:text-[#b4dcf5]',
    ring: 'ring-[#c0ddf0] dark:ring-[#2a5a7a]',
  },
}

/**
 * Tooltip text per type. Operators hovering should immediately understand
 * (a) what the badge is asserting and (b) that it's heuristic. Especially
 * important for `Self-hosted` — a cloud cluster behind custom DNS will land
 * here and we don't want operators thinking it's mis-registered.
 */
const TYPE_TOOLTIP: Record<ClusterType, string> = {
  EKS: 'AWS EKS cluster (detected from API server hostname)',
  AKS: 'Azure AKS cluster (detected from API server hostname)',
  GKE: 'Google GKE cluster (detected from API server hostname)',
  kind: 'kind cluster (local development) — detected from API server hostname',
  minikube:
    'minikube cluster (local development) — detected from API server hostname',
  'Self-hosted':
    'Self-hosted or unrecognized cluster — hostname did not match a known cloud or dev pattern',
}

export function ClusterTypeBadge({
  server,
  compact,
}: ClusterTypeBadgeProps) {
  const type = classifyClusterType(server)
  const tone = TYPE_TONE[type]
  const tooltip = TYPE_TOOLTIP[type]

  const sizeClasses = compact
    ? 'px-2 py-0.5 text-xs'
    : 'px-2.5 py-0.5 text-xs'

  return (
    <span
      data-cluster-type={type}
      className={`inline-flex items-center rounded-full font-medium ring-1 ${tone.base} ${tone.ring} ${sizeClasses}`}
      title={tooltip}
      aria-label={`Cluster type: ${type}`}
    >
      {type}
    </span>
  )
}

export default ClusterTypeBadge
