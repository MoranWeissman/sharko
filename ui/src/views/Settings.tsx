import { useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { DetailNavPanel } from '@/components/DetailNavPanel'
import { Connections } from '@/views/Connections'
import { UserManagement } from '@/views/UserManagement'
import { ApiKeys } from '@/views/ApiKeys'
import { useAuth } from '@/hooks/useAuth'

export function Settings() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const section = searchParams.get('section') || 'connections'
  const setSection = (s: string) => setSearchParams({ section: s }, { replace: true })

  useEffect(() => {
    if (!isAdmin && section !== 'connections') {
      setSearchParams({ section: 'connections' }, { replace: true })
    }
  }, [isAdmin, section, setSearchParams])

  const sections = [
    {
      label: 'Connections',
      items: [{ key: 'connections', label: 'Connections' }],
    },
    ...(isAdmin
      ? [
          {
            label: 'Access',
            items: [
              { key: 'users', label: 'Users' },
              { key: 'api-keys', label: 'API Keys' },
            ],
          },
        ]
      : []),
    {
      label: 'Platform',
      items: [{ key: 'ai', label: 'AI Provider' }],
    },
  ]

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Settings</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Manage connections, users, API keys, and platform configuration.
        </p>
      </div>

      <div className="flex gap-0 rounded-xl border border-[#90c8ee] bg-white overflow-hidden dark:border-gray-700 dark:bg-gray-800">
        <DetailNavPanel
          sections={sections}
          activeKey={section}
          onSelect={setSection}
        />
        <div className="flex-1 p-6">
          {section === 'connections' && <Connections embedded />}
          {section === 'users' && isAdmin && <UserManagement embedded />}
          {section === 'api-keys' && isAdmin && <ApiKeys embedded />}
          {section === 'ai' && (
            <div className="text-sm text-gray-500 dark:text-gray-400">
              AI Provider configuration coming soon.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
