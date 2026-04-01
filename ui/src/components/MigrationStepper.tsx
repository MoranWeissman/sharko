import type { MigrationStep, Migration } from '@/services/api'
import { MigrationStepCard } from '@/components/MigrationStep'
import { CheckCircle } from 'lucide-react'
import { Button } from '@/components/ui/button'

interface MigrationStepperProps {
  steps: MigrationStep[]
  currentStep: number
  migrationStatus: Migration['status']
  migrationId: string
  onContinue: () => void
  onRetry: () => void
  onMergePR: (step: number) => void
}

export function MigrationStepper({ steps, currentStep, migrationStatus, onContinue, onRetry, onMergePR }: MigrationStepperProps) {
  const isGated = migrationStatus === 'gated'

  return (
    <div>
      {steps.map((step, index) => {
        const isActive = step.number === currentStep
        const isLast = index === steps.length - 1

        // Show gate AFTER the last completed step (before the next pending one)
        // Gate appears when: migration is gated, this step is completed, and it's the step just before currentStep
        const showGateAfter = isGated && step.status === 'completed' && step.number === currentStep - 1

        return (
          <div key={step.number}>
            <MigrationStepCard
              step={step}
              isActive={isActive}
              isLast={isLast && !showGateAfter}
              onContinue={onContinue}
              onRetry={onRetry}
              onMergePR={onMergePR}
            />

            {/* Gate approval — appears AFTER the completed step */}
            {showGateAfter && (
              <div className="flex gap-3 mb-2">
                <div className="flex flex-col items-center">
                  <div className="w-0.5 flex-1 border-l-2 border-dashed border-amber-400" />
                </div>
                <div className="flex flex-1 items-center gap-3 rounded-md border border-amber-400 bg-amber-50 px-3 py-2 dark:border-amber-600 dark:bg-amber-900/20">
                  <div className="flex-1">
                    <p className="text-xs font-semibold text-amber-700 dark:text-amber-400">
                      Awaiting approval to continue
                    </p>
                  </div>
                  <Button size="sm" onClick={onContinue} className="h-7 bg-amber-600 hover:bg-amber-700 text-xs">
                    <CheckCircle className="h-3 w-3" />
                    Approve
                  </Button>
                </div>
              </div>
            )}
          </div>
        )
      })}
    </div>
  )
}
