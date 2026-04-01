import { createContext, useContext, useState, useEffect, useCallback } from 'react'
import type { ReactNode } from 'react'
import { api } from '@/services/api'
import type { ConnectionResponse } from '@/services/models'

interface ConnectionContextValue {
  connections: ConnectionResponse[]
  activeConnection: string | null
  loading: boolean
  error: string | null
  setActiveConnection: (name: string) => Promise<void>
  refreshConnections: () => void
}

const ConnectionContext = createContext<ConnectionContextValue | null>(null)

export function ConnectionProvider({ children }: { children: ReactNode }) {
  const [connections, setConnections] = useState<ConnectionResponse[]>([])
  const [activeConnection, setActive] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const fetchConnections = useCallback(() => {
    setLoading(true)
    setError(null)
    api.getConnections()
      .then(res => {
        setConnections(res.connections)
        setActive(res.active_connection || (res.connections.length > 0 ? res.connections[0].name : null))
      })
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { fetchConnections() }, [fetchConnections])

  const setActiveConnection = useCallback(async (name: string) => {
    await api.setActiveConnection(name)
    setActive(name)
    fetchConnections()
  }, [fetchConnections])

  return (
    <ConnectionContext value={{
      connections,
      activeConnection,
      loading,
      error,
      setActiveConnection,
      refreshConnections: fetchConnections,
    }}>
      {children}
    </ConnectionContext>
  )
}

export function useConnections() {
  const ctx = useContext(ConnectionContext)
  if (!ctx) throw new Error('useConnections must be used within ConnectionProvider')
  return ctx
}
