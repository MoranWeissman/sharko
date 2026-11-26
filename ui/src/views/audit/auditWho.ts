/**
 * auditWho — the text shown in the "Who" column of the audit log, on screen
 * and in the CSV export.
 *
 * A call made with an API key is marked as one. The name on such a row is
 * the name of the key, not a person, and the reader has to be able to see
 * that at a glance.
 */
export const API_KEY_MARK = '(API key)'

export function auditWho(entry: { user?: string; auth_type?: string }): string {
  const name = entry.user || 'anonymous'
  return entry.auth_type === 'api_key' ? `${name} ${API_KEY_MARK}` : name
}
