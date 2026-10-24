/**
 * A Kubernetes version for display. The server sends it with its own "v"
 * ("v1.31.0"); older answers and some providers send it bare ("1.31.0").
 * Show exactly one "v" either way, never "vv1.31.0".
 */
export function withV(version: string): string {
  return /^v/i.test(version) ? version : `v${version}`
}
