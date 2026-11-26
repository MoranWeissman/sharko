/**
 * Fired on `window` after the AI settings were saved or switched off. The
 * page frame (Layout) listens and asks the server again whether AI is on, so
 * the "Ask AI" buttons come and go right away. Before this they only changed
 * after a page reload.
 */
export const AI_CONFIG_CHANGED_EVENT = 'sharko:ai-config-changed'

export function announceAIConfigChanged(): void {
  window.dispatchEvent(new Event(AI_CONFIG_CHANGED_EVENT))
}
