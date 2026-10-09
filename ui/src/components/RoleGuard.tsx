import { useContext } from 'react'
import type { ReactNode } from 'react'
import { AuthContext } from '../hooks/useAuth'

interface RoleGuardProps {
  children: ReactNode
  roles?: string[]   // e.g. ['admin', 'operator']
  adminOnly?: boolean
  fallback?: ReactNode // rendered instead of children when access denied
}

export function RoleGuard({ children, roles, adminOnly, fallback }: RoleGuardProps) {
  const ctx = useContext(AuthContext)
  // Outside AuthProvider — deny access (safe default for security boundary)
  if (!ctx) return <>{fallback ?? null}</>
  const { role, isAdmin } = ctx
  if (adminOnly && !isAdmin) return <>{fallback ?? null}</>
  if (roles && !roles.includes(role || '')) return <>{fallback ?? null}</>
  return <>{children}</>
}
