import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { ToastContainer, showToast } from '@/components/ToastNotification'

// Use fake timers so the auto-dismiss setTimeout doesn't interfere.
beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('ToastNotification', () => {
  it('renders a success toast with a green icon', () => {
    render(<ToastContainer />)
    act(() => { showToast('Operation succeeded', 'success') })

    const msg = screen.getByText('Operation succeeded')
    expect(msg).toBeInTheDocument()

    // The CheckCircle2 SVG is the sibling container's icon — verify red class absent.
    const container = msg.closest('div.flex.items-start')!
    expect(container.querySelector('.text-green-500')).toBeInTheDocument()
    expect(container.querySelector('.text-red-500')).not.toBeInTheDocument()
  })

  it('renders an info toast with a teal icon', () => {
    render(<ToastContainer />)
    act(() => { showToast('Just so you know', 'info') })

    const msg = screen.getByText('Just so you know')
    expect(msg).toBeInTheDocument()

    const container = msg.closest('div.flex.items-start')!
    expect(container.querySelector('.text-teal-500')).toBeInTheDocument()
    expect(container.querySelector('.text-red-500')).not.toBeInTheDocument()
  })

  it('renders an error toast with a red icon', () => {
    render(<ToastContainer />)
    act(() => { showToast('Failed to restart sync: ArgoCD unavailable', 'error') })

    const msg = screen.getByText('Failed to restart sync: ArgoCD unavailable')
    expect(msg).toBeInTheDocument()

    const container = msg.closest('div.flex.items-start')!
    expect(container.querySelector('.text-red-500')).toBeInTheDocument()
    expect(container.querySelector('.text-green-500')).not.toBeInTheDocument()
    expect(container.querySelector('.text-teal-500')).not.toBeInTheDocument()
  })

  it('defaults to success type when no type is given', () => {
    render(<ToastContainer />)
    act(() => { showToast('Default toast') })

    const msg = screen.getByText('Default toast')
    expect(msg).toBeInTheDocument()

    const container = msg.closest('div.flex.items-start')!
    expect(container.querySelector('.text-green-500')).toBeInTheDocument()
  })
})
