// SecretsSubnav — Secrets-area rename (SN-3). The navigation between the
// two Secrets subpages. It looks like tabs but is built from real links on
// purpose: each item is a genuine URL (direct load, refresh, Back/Forward
// and shared links all work), so links are the honest element and the
// keyboard works for free. NOT an ARIA tablist — that would claim an
// in-page widget this is not. NavLink marks the current one with
// aria-current="page" on its own.
//
// 390px rule: the strip must never make the page scroll sideways — the
// items wrap if they don't fit (flex-wrap), the page body never grows.

import { NavLink } from 'react-router-dom'

const itemClass = ({ isActive }: { isActive: boolean }) =>
  `-mb-px border-b-2 px-3 py-2 text-sm font-medium transition-colors ${
    isActive
      ? 'border-[#1a3d5c] text-[#0a2a4a] dark:border-blue-400 dark:text-gray-100'
      : 'border-transparent text-[#3a6a8a] hover:border-[#6aade0] hover:text-[#0a2a4a] dark:text-gray-400 dark:hover:border-gray-600 dark:hover:text-gray-200'
  }`

export function SecretsSubnav() {
  return (
    <nav aria-label="Secrets" className="flex flex-wrap gap-1 border-b border-[#6aade0] dark:border-gray-800">
      <NavLink to="/secrets/connections" className={itemClass}>
        Cluster connections
      </NavLink>
      <NavLink to="/secrets/addons" className={itemClass}>
        Addon secrets
      </NavLink>
    </nav>
  )
}

export default SecretsSubnav
