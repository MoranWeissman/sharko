import { useSearchParams } from 'react-router-dom'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { Connections } from '@/views/Connections'
import { UserManagement } from '@/views/UserManagement'
import { ApiKeys } from '@/views/ApiKeys'
import { useAuth } from '@/hooks/useAuth'

export function Settings() {
  const [searchParams, setSearchParams] = useSearchParams()
  const { isAdmin } = useAuth()
  const tab = searchParams.get('tab') || 'connections'
  const setTab = (t: string) => setSearchParams({ tab: t }, { replace: true })

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-bold text-gray-900 dark:text-gray-100">Settings</h1>
        <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
          Manage connections, users, API keys, and platform configuration.
        </p>
      </div>

      <Tabs value={tab} onValueChange={setTab}>
        <TabsList variant="line">
          <TabsTrigger value="connections">Connections</TabsTrigger>
          {isAdmin && <TabsTrigger value="users">Users</TabsTrigger>}
          {isAdmin && <TabsTrigger value="api-keys">API Keys</TabsTrigger>}
        </TabsList>

        <TabsContent value="connections">
          <Connections embedded />
        </TabsContent>

        {isAdmin && (
          <TabsContent value="users">
            <UserManagement embedded />
          </TabsContent>
        )}

        {isAdmin && (
          <TabsContent value="api-keys">
            <ApiKeys embedded />
          </TabsContent>
        )}
      </Tabs>
    </div>
  )
}
