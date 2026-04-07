import { useEffect } from 'react'
import { useSearchParams } from 'react-router-dom'
import { Plug, Users, Key, Bot } from 'lucide-react'
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
      label: 'Connection',
      items: [{ key: 'connections', label: 'Connection', icon: Plug }],
    },
    ...(isAdmin
      ? [
          {
            label: 'Access',
            items: [
              { key: 'users', label: 'Users', icon: Users },
              { key: 'api-keys', label: 'API Keys', icon: Key },
            ],
          },
        ]
      : []),
    {
      label: 'Platform',
      items: [{ key: 'ai', label: 'AI Provider', icon: Bot }],
    },
  ]

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-bold text-[#0a2a4a] dark:text-gray-100">Settings</h1>
        <p className="mt-1 text-sm text-[#2a5a7a] dark:text-gray-400">
          Manage connections, users, API keys, and platform configuration.
        </p>
      </div>

      <div className="flex gap-6 mt-4">
        <DetailNavPanel
          sections={sections}
          activeKey={section}
          onSelect={setSection}
        />
        <div className="flex-1">
          {section === 'connections' && <Connections embedded />}
          {section === 'users' && isAdmin && <UserManagement embedded />}
          {section === 'api-keys' && isAdmin && <ApiKeys embedded />}
          {section === 'ai' && (
            <div className="text-sm text-[#2a5a7a] dark:text-gray-400">
              AI Provider configuration coming soon.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
export default Settings
