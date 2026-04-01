import {
  CheckCircle2,
  Loader2,
  Clock,
  XCircle,
  ExternalLink,
  SkipForward,
} from 'lucide-react'
import type { MigrationStep as MigrationStepType } from '@/services/api'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'

interface MigrationStepProps {
  step: MigrationStepType
  isActive: boolean
  isLast: boolean
  migrationId?: string
  onContinue?: () => void
  onRetry?: () => void
  onMergePR?: (step: number) => void
}

function StepIcon({ status, number }: { status: MigrationStepType['status']; number: number }) {
  const base = 'flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-[10px] font-bold'

  switch (status) {
    case 'completed':
      return <div className={cn(base, 'bg-green-500 text-white')}><CheckCircle2 className="h-3.5 w-3.5" /></div>
    case 'running':
      return <div className={cn(base, 'bg-blue-500 text-white')}><Loader2 className="h-3.5 w-3.5 animate-spin" /></div>
    case 'waiting':
      return <div className={cn(base, 'bg-amber-500 text-white')}><Clock className="h-3.5 w-3.5" /></div>
    case 'failed':
      return <div className={cn(base, 'bg-red-500 text-white')}><XCircle className="h-3.5 w-3.5" /></div>
    case 'skipped':
      return <div className={cn(base, 'bg-gray-400 text-white')}><SkipForward className="h-3 w-3" /></div>
    default:
      return <div className={cn(base, 'bg-gray-300 text-gray-600 dark:bg-gray-600 dark:text-gray-300')}>{number}</div>
  }
}

export function MigrationStepCard({ step, isActive, isLast, onContinue, onRetry, onMergePR }: MigrationStepProps) {
  const isCompleted = step.status === 'completed'
  const isFailed = step.status === 'failed'
  const isWaiting = step.status === 'waiting'

  return (
    <div className="flex gap-3">
      {/* Left: icon + connecting line */}
      <div className="flex flex-col items-center">
        <StepIcon status={step.status} number={step.number} />
        {!isLast && (
          <div className={cn(
            'mt-0.5 w-0.5 flex-1 min-h-[12px]',
            isCompleted ? 'bg-green-400' : 'border-l border-dashed border-gray-300 dark:border-gray-600'
          )} />
        )}
      </div>

      {/* Right: compact content */}
      <div className={cn(
        'mb-2 flex-1 rounded-md px-3 py-2',
        isActive && !isFailed && 'bg-blue-50/80 dark:bg-blue-900/20',
        isFailed && 'bg-red-50/80 dark:bg-red-900/20',
        isCompleted && 'opacity-50',
      )}>
        <div className="flex items-center justify-between">
          <span className={cn(
            'text-sm',
            isActive ? 'font-semibold text-gray-900 dark:text-gray-100' : 'text-gray-700 dark:text-gray-300',
            isCompleted && 'text-gray-500 dark:text-gray-500'
          )}>
            {step.title}
          </span>

          {/* Duration */}
          {step.started_at && step.completed_at && (
            <span className="text-[10px] text-gray-400">
              {Math.round((new Date(step.completed_at).getTime() - new Date(step.started_at).getTime()) / 1000)}s
            </span>
          )}
          {step.started_at && !step.completed_at && step.status === 'running' && (
            <span className="text-[10px] text-blue-400">running...</span>
          )}
        </div>

        {/* Expanded details for active/failed/waiting steps */}
        {(isActive || isFailed || isWaiting) && (
          <div className="mt-1.5 space-y-1.5">
            {step.message && (
              <p className="text-xs text-gray-600 dark:text-gray-400">{step.message}</p>
            )}

            {step.pr_url && (
              <a href={step.pr_url} target="_blank" rel="noopener noreferrer"
                className="inline-flex items-center gap-1 text-xs font-medium text-blue-600 hover:text-blue-800 dark:text-blue-400">
                <ExternalLink className="h-3 w-3" />
                Pull Request {step.pr_status && `(${step.pr_status})`}
              </a>
            )}

            {isWaiting && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-amber-600 dark:text-amber-400">Waiting for PR merge</span>
                {onMergePR && step.pr_number && (
                  <Button size="sm" variant="outline" onClick={() => onMergePR(step.number)} className="h-6 px-2 text-xs bg-green-50 border-green-300 text-green-700 hover:bg-green-100 dark:bg-green-900/20 dark:border-green-700 dark:text-green-400">
                    Merge PR
                  </Button>
                )}
                {onContinue && (
                  <Button size="sm" variant="outline" onClick={onContinue} className="h-6 px-2 text-xs">
                    PR Merged → Continue
                  </Button>
                )}
              </div>
            )}

            {step.error && (
              <div className="flex items-center gap-2">
                <span className="text-xs text-red-600 dark:text-red-400">{step.error}</span>
                {onRetry && (
                  <Button size="sm" variant="destructive" onClick={onRetry} className="h-6 px-2 text-xs">
                    Retry
                  </Button>
                )}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  )
}
