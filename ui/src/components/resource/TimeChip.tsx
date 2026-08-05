// TimeChip — the house "when did this last happen" cell (S1.4). Builds on
// the relative-time formatting `WhenCell` already used (ManagedSecrets'
// old per-section rows) and keeps its best part: the exact timestamp on
// hover, via a plain `title` attribute — no new tooltip machinery needed.
//
// S7.4 fix: a MISSING timestamp is not the same word as the "not checked
// yet" STATE (see StatusMark). Mixing them ("Unknown" for both) makes a
// reader wonder which one a given "Unknown" means. TimeChip renders a
// missing timestamp as an em dash with a hover explanation instead of
// reusing the word "Unknown" for anything.

import { relativeTime } from '@/lib/time'

export function TimeChip({ iso, className }: { iso?: string; className?: string }) {
  if (!iso) {
    return (
      <span
        className={`text-[#5a8aaa] dark:text-gray-500 ${className ?? ''}`}
        title="Sharko hasn't recorded a time for this yet."
      >
        —
      </span>
    )
  }
  return (
    <span title={iso} className={`text-[#2a5a7a] dark:text-gray-300 ${className ?? ''}`}>
      {relativeTime(iso)}
    </span>
  )
}

export default TimeChip
