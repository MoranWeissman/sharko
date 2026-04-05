import { type ReactNode } from 'react'

interface EmptyStateProps {
  title: string
  description?: string
  action?: ReactNode
}

export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center gap-4 py-16 text-center">
      <img
        src="/sharko-mascot.png"
        alt=""
        className="h-20 w-auto opacity-80"
      />
      <div>
        <h3 className="text-lg font-semibold text-[#0a2a4a] dark:text-gray-100">{title}</h3>
        {description && (
          <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">{description}</p>
        )}
      </div>
      {action}
    </div>
  )
}
