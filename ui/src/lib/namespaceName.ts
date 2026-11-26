/**
 * A namespace name Kubernetes accepts: lowercase letters, digits and hyphens,
 * starting and ending with a letter or digit, 63 characters at most.
 *
 * One rule in one place. The server checks the same rule (models.
 * IsValidNamespaceName) and gives the same sentence, so the screen and the
 * API never disagree.
 */
const NAMESPACE_RE = /^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$/

export const NAMESPACE_RULE_SENTENCE =
  'A namespace can only use lowercase letters, digits and hyphens. It must start and end with a letter or digit, and be 63 characters at most.'

export function isValidNamespace(ns: string): boolean {
  return NAMESPACE_RE.test(ns)
}
