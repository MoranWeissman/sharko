import { describe, it, expect } from 'vitest'
import { render } from '@testing-library/react'
import { Dialog, DialogContent } from '../components/ui/dialog'

describe('Dialog - Overflow Prevention', () => {
  it('DialogContent has grid-cols-[minmax(0,1fr)] to prevent overflow', () => {
    render(
      <Dialog open={true}>
        <DialogContent>
          <div>Test content</div>
        </DialogContent>
      </Dialog>
    )

    // Dialog content is rendered in a portal, so query from document.body
    const dialogContent = document.body.querySelector('[data-slot="dialog-content"]')
    expect(dialogContent).toBeTruthy()

    // Check that the grid-cols-[minmax(0,1fr)] class is present
    const className = dialogContent?.className || ''
    expect(className).toContain('grid-cols-[minmax(0,1fr)]')
  })

  it('DialogContent has grid class for layout', () => {
    render(
      <Dialog open={true}>
        <DialogContent>
          <div>Test content</div>
        </DialogContent>
      </Dialog>
    )

    const dialogContent = document.body.querySelector('[data-slot="dialog-content"]')
    const className = dialogContent?.className || ''
    expect(className).toContain('grid')
  })

  it('DialogContent has min-w-0 to allow shrinking', () => {
    render(
      <Dialog open={true}>
        <DialogContent>
          <div>Test content</div>
        </DialogContent>
      </Dialog>
    )

    const dialogContent = document.body.querySelector('[data-slot="dialog-content"]')
    const className = dialogContent?.className || ''
    expect(className).toContain('min-w-0')
  })
})
