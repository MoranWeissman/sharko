import type { ComponentType } from 'react'
import { useSearchParams } from 'react-router-dom'

interface NavGroup {
  label?: string
  items: NavPanelItem[]
}

interface NavPanelItem {
  key: string
  label: string
  icon?: ComponentType<{ className?: string }>
  badge?: string | number
  destructive?: boolean
}

interface DetailNavPanelProps {
  sections: NavGroup[]
  activeKey: string
  onSelect: (key: string) => void
}

export function DetailNavPanel({ sections, activeKey, onSelect }: DetailNavPanelProps) {
  return (
    <div className="w-48 shrink-0">
      <nav className="p-3 space-y-4">
        {sections.map((group, gi) => (
          <div key={gi}>
            {group.label && (
              <p className="mb-1 px-2 text-[9px] font-semibold uppercase tracking-wider text-[#3a6a8a] dark:text-gray-500">
                {group.label}
              </p>
            )}
            <div className="space-y-0.5">
              {group.items.map((item) => (
                <button
                  key={item.key}
                  onClick={() => onSelect(item.key)}
                  className={`flex w-full items-center justify-between rounded-md px-3 py-2 text-left text-sm transition-colors ${
                    activeKey === item.key
                      ? 'border-l-[3px] border-[#0a2a4a] bg-[#d6eeff] font-semibold text-[#0a2a4a] dark:border-blue-400 dark:bg-gray-700 dark:text-white'
                      : item.destructive
                        ? 'border-l-[3px] border-transparent text-red-600 hover:bg-[#d6eeff] dark:text-red-400 dark:hover:bg-red-900/20'
                        : 'border-l-[3px] border-transparent text-[#1a4a6a] hover:bg-[#d6eeff] dark:text-gray-400 dark:hover:bg-gray-700'
                  }`}
                >
                  <span className="flex items-center gap-2">
                    {item.icon && <item.icon className="h-4 w-4 shrink-0" />}
                    <span>{item.label}</span>
                  </span>
                  {item.badge !== undefined && (
                    <span className="rounded-full bg-[#d6eeff] px-1.5 py-0.5 text-[10px] font-medium text-[#2a5a7a] dark:bg-gray-700 dark:text-gray-400">
                      {item.badge}
                    </span>
                  )}
                </button>
              ))}
            </div>
          </div>
        ))}
      </nav>
    </div>
  )
}

export function useDetailSection(defaultSection: string) {
  const [searchParams, setSearchParams] = useSearchParams()
  const section = searchParams.get('section') || defaultSection
  const setSection = (s: string) => setSearchParams({ section: s }, { replace: true })
  return [section, setSection] as const
}
