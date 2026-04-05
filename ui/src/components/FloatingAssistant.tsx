import { MessageSquare } from 'lucide-react'

export function FloatingAssistant() {
  return (
    <button
      onClick={() => window.dispatchEvent(new CustomEvent('open-assistant'))}
      className="fixed bottom-6 right-6 z-50 flex h-14 w-14 items-center justify-center rounded-full bg-gradient-to-br from-teal-500 to-blue-600 shadow-lg transition-all duration-200 hover:from-teal-600 hover:to-blue-700 hover:shadow-xl"
      aria-label="Open AI Assistant"
    >
      <MessageSquare className="h-6 w-6 text-white" />
    </button>
  )
}
