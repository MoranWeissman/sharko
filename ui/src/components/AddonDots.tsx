import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from '@/components/ui/tooltip'

interface AddonDot {
  name: string
  health: string
}

const healthColor: Record<string, string> = {
  Healthy: 'bg-green-500',
  Progressing: 'bg-amber-400',
  Degraded: 'bg-red-500',
  Missing: 'bg-[#90c8ee]',
  Unknown: 'bg-[#90c8ee]',
}

// LW-2: Severity order for sorting (unhealthy first)
const healthSeverity: Record<string, number> = {
  Degraded: 0,
  Missing: 1,
  Progressing: 2,
  Unknown: 3,
  Healthy: 4,
}

interface AddonDotsProps {
  addons: AddonDot[]
}

// LW-2: Summarize addon health instead of rendering one dot per addon.
// For large addon counts, show a compact count line ("2 degraded · 1 progressing")
// OR a capped strip (unhealthy-first, up to 6 dots + "+N more"). Lead with unhealthy.
// Fixed-height card regardless of addon count.
export function AddonDots({ addons }: AddonDotsProps) {
  if (addons.length === 0) return null

  // Count by health status
  const counts: Record<string, number> = {}
  for (const addon of addons) {
    const health = addon.health || 'Unknown'
    counts[health] = (counts[health] || 0) + 1
  }

  // If <= 6 addons total, render all dots (sorted unhealthy-first, with tooltips)
  if (addons.length <= 6) {
    const sorted = [...addons].sort((a, b) => {
      const sevA = healthSeverity[a.health] ?? 99
      const sevB = healthSeverity[b.health] ?? 99
      return sevA - sevB
    })
    return (
      <TooltipProvider delayDuration={200}>
        <div className="flex flex-wrap gap-1">
          {sorted.map((addon) => (
            <Tooltip key={addon.name}>
              <TooltipTrigger asChild>
                <span
                  className={`inline-block h-2.5 w-2.5 rounded-full transition-colors ${healthColor[addon.health] ?? 'bg-[#90c8ee]'}`}
                  tabIndex={0}
                  role="img"
                  aria-label={`${addon.name}: ${addon.health}`}
                />
              </TooltipTrigger>
              <TooltipContent side="top" className="text-xs">
                <span className="font-medium">{addon.name}</span>: {addon.health}
              </TooltipContent>
            </Tooltip>
          ))}
        </div>
      </TooltipProvider>
    )
  }

  // For large counts, show a compact count line (non-zero categories only, unhealthy first)
  const parts: string[] = []
  const orderedStates = ['Degraded', 'Missing', 'Progressing', 'Unknown', 'Healthy']
  for (const state of orderedStates) {
    const count = counts[state]
    if (count && count > 0) {
      const label = state.toLowerCase()
      parts.push(`${count} ${label}`)
    }
  }

  return (
    <p className="text-xs text-[#2a5a7a] dark:text-gray-400">
      {parts.join(' · ')}
    </p>
  )
}
