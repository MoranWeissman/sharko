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
  Missing: 'bg-gray-400',
  Unknown: 'bg-gray-400',
}

interface AddonDotsProps {
  addons: AddonDot[]
}

export function AddonDots({ addons }: AddonDotsProps) {
  if (addons.length === 0) return null

  return (
    <TooltipProvider delayDuration={200}>
      <div className="flex flex-wrap gap-1">
        {addons.map((addon) => (
          <Tooltip key={addon.name}>
            <TooltipTrigger asChild>
              <span
                className={`inline-block h-2.5 w-2.5 rounded-full transition-colors ${healthColor[addon.health] ?? 'bg-gray-400'}`}
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
