import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { NotFound, NOT_FOUND_TITLE, NOT_FOUND_DESCRIPTION, NOT_FOUND_LINK } from '@/views/NotFound'
import { ADMIN_LEGEND, OPERATOR_LEGEND } from '@/views/UserManagement'

// Walk 10: an address Sharko does not have gave a fully black screen.
describe('NotFound', () => {
  it('says so in plain words and links to the Dashboard', () => {
    expect(NOT_FOUND_TITLE).toBe('This page does not exist')
    expect(NOT_FOUND_DESCRIPTION).toBe('The address may be mistyped, or the page may have moved.')
    render(
      <MemoryRouter>
        <NotFound />
      </MemoryRouter>,
    )
    expect(screen.getByText('This page does not exist')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: NOT_FOUND_LINK })).toHaveAttribute('href', '/dashboard')
  })
})

// Walk 5, L8: the legend said an operator can do "all except user management".
describe('Users page role legend', () => {
  it('pins the sentences and bans the old one', () => {
    expect(ADMIN_LEGEND).toBe('— everything, including users, keys and settings')
    expect(OPERATOR_LEGEND).toBe(
      '— register and adopt clusters, turn addons on and off, add addons to the catalog. Cannot remove things, change settings or manage users',
    )
    expect(OPERATOR_LEGEND).not.toMatch(/all except user management/)
  })
})
