import { useContext } from 'react'
import type { ReactNode } from 'react'
import { AuthContext } from '../hooks/useAuth'

interface RoleGuardProps {
  children: ReactNode
  roles?: string[]   // e.g. ['admin', 'operator']
  adminOnly?: boolean
}

export function RoleGuard({ children, roles, adminOnly }: RoleGuardProps) {
  const ctx = useContext(AuthContext)
  // Outside AuthProvider (e.g. in tests) — fall through and render
  if (!ctx) return <>{children}</>
  const { role, isAdmin } = ctx
  if (adminOnly && !isAdmin) return null
  if (roles && !roles.includes(role || '')) return null
  return <>{children}</>
}
