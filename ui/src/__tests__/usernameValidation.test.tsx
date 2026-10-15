import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import UserManagement from '../views/UserManagement'

describe('UserManagement - Username Validation', () => {
  it('shows the correct validation message for username requirements', () => {
    render(<UserManagement />)

    // Click "Add User" to show the form
    const addButton = screen.getByRole('button', { name: /add user/i })
    addButton.click()

    // Find the validation helper text
    const helperText = screen.getByText(
      'A user name can use letters, numbers, dots, dashes and underscores, with no spaces.'
    )
    expect(helperText).toBeTruthy()
  })

  it('shows the exact error message text', () => {
    const expectedMessage = 'A user name can use letters, numbers, dots, dashes and underscores, with no spaces.'

    render(<UserManagement />)

    const addButton = screen.getByRole('button', { name: /add user/i })
    addButton.click()

    const helperText = screen.getByText(expectedMessage)
    expect(helperText.textContent).toBe(expectedMessage)
  })

  it('username input field is present with correct placeholder', () => {
    render(<UserManagement />)

    const addButton = screen.getByRole('button', { name: /add user/i })
    addButton.click()

    const usernameInput = screen.getByPlaceholderText(/e.g. john.doe/i)
    expect(usernameInput).toBeTruthy()
    expect(usernameInput.getAttribute('type')).toBe('text')
  })
})
