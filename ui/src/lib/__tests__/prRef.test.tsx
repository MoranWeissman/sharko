import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { prRef, prNumberPrefix, hasPRNumber, isFinishingPR } from '@/lib/prRef'
import { prStatusBadge } from '@/components/PendingPRsPanel'

// EP3 round S shared contract: never "PR #0".
describe('prRef', () => {
  it('shows the number only when it is above zero', () => {
    expect(prRef(12)).toBe('PR #12')
    expect(prRef(0)).toBe('a pull request')
    expect(prRef(undefined)).toBe('a pull request')
    expect(prRef(-3, 'PR')).toBe('PR')
    expect(prRef(Number.NaN, 'Pull request')).toBe('Pull request')
    expect(prNumberPrefix(7)).toBe('#7 ')
    expect(prNumberPrefix(0)).toBe('')
    expect(hasPRNumber('12')).toBe(false)
  })

  it('reads status "finishing" from status, or from last_status', () => {
    expect(isFinishingPR({ status: 'finishing', last_status: 'merged' })).toBe(true)
    expect(isFinishingPR({ last_status: 'finishing' })).toBe(true)
    expect(isFinishingPR({ last_status: 'merged' })).toBe(false)
  })

  it('the Pending pull requests panel badge says Finishing, plain, no spinner', () => {
    render(<div>{prStatusBadge('finishing')}</div>)
    const label = screen.getByTestId('pr-finishing')
    expect(label).toHaveTextContent(/^Finishing$/)
    expect(label.querySelector('svg')).toBeNull()
  })
})
