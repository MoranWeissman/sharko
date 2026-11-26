import { useContext } from 'react'
import type { ReactNode } from 'react'
import { AuthContext } from '../hooks/useAuth'

interface RoleGuardProps {
  children: ReactNode
  roles?: string[]   // e.g. ['admin', 'operator']
  adminOnly?: boolean
  fallback?: ReactNode // rendered instead of children when access denied
}

/**
 * The roles that may change things Sharko manages: register or adopt a
 * cluster, turn an add-on on or off, add an add-on to the catalog. The
 * server's rule table (internal/authz) gives these to an operator as well as
 * an admin; only removing, un-adopting, taking over, users, keys and the
 * settings switches are admin-only. Screens that offer one of the operator
 * actions use this list, so the button is there for everyone the API lets
 * press it.
 */
export const OPERATOR_AND_UP = ['admin', 'operator']

export function RoleGuard({ children, roles, adminOnly, fallback }: RoleGuardProps) {
  const ctx = useContext(AuthContext)
  // Outside AuthProvider — deny access (safe default for security boundary)
  if (!ctx) return <>{fallback ?? null}</>
  const { role, isAdmin } = ctx
  if (adminOnly && !isAdmin) return <>{fallback ?? null}</>
  if (roles && !roles.includes(role || '')) return <>{fallback ?? null}</>
  return <>{children}</>
}
