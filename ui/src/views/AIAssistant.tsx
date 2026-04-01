import { useState, useRef, useEffect, useCallback } from 'react'
import { Sparkles, Send, User, RotateCcw, RefreshCw, Wrench, Database, Search, Shield, Download } from 'lucide-react'
import { api } from '@/services/api'

interface ChatMessage {
  id: string
  role: 'user' | 'assistant'
  content: string
  timestamp: Date
  streaming?: boolean
}

// ---------------------------------------------------------------------------
// Suggested prompts
// ---------------------------------------------------------------------------

const SUGGESTED_PROMPTS = [
  'What addons are deployed across my clusters?',
  'Is everything healthy right now?',
  'Compare datadog versions across clusters',
  'Should I upgrade istio-base to the latest version?',
  'Show me the config overrides for devops-automation-dev-eks',
  'Which clusters have the most addons?',
]

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function formatRelativeTime(date: Date): string {
  const seconds = Math.floor((Date.now() - date.getTime()) / 1000)
  if (seconds < 10) return 'just now'
  if (seconds < 60) return `${seconds}s ago`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`
  const days = Math.floor(hours / 24)
  return `${days}d ago`
}

// ---------------------------------------------------------------------------
// Markdown renderer
// ---------------------------------------------------------------------------

function renderFormattedContent(content: string) {
  const lines = content.split('\n')
  const elements: React.ReactNode[] = []
  let listBuffer: { type: 'ul' | 'ol'; items: React.ReactNode[] } | null = null
  let codeBlockBuffer: string[] | null = null

  function flushList() {
    if (listBuffer) {
      if (listBuffer.type === 'ul') {
        elements.push(
          <ul key={`list-${elements.length}`} className="ml-4 list-disc space-y-1">
            {listBuffer.items.map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ul>,
        )
      } else {
        elements.push(
          <ol key={`list-${elements.length}`} className="ml-4 list-decimal space-y-1">
            {listBuffer.items.map((item, i) => (
              <li key={i}>{item}</li>
            ))}
          </ol>,
        )
      }
      listBuffer = null
    }
  }

  function flushCodeBlock() {
    if (codeBlockBuffer) {
      elements.push(
        <pre
          key={`code-${elements.length}`}
          className="overflow-x-auto rounded-lg bg-gray-900 p-3 text-xs text-gray-100 dark:bg-gray-950"
        >
          <code>{codeBlockBuffer.join('\n')}</code>
        </pre>,
      )
      codeBlockBuffer = null
    }
  }

  function formatInline(text: string): React.ReactNode {
    const parts: React.ReactNode[] = []
    const regex = /(\*\*(.+?)\*\*|`(.+?)`)/g
    let lastIndex = 0
    let match: RegExpExecArray | null

    while ((match = regex.exec(text)) !== null) {
      if (match.index > lastIndex) {
        parts.push(text.slice(lastIndex, match.index))
      }
      if (match[2]) {
        parts.push(<strong key={match.index}>{match[2]}</strong>)
      } else if (match[3]) {
        parts.push(
          <code
            key={match.index}
            className="break-all rounded bg-gray-200 px-1 py-0.5 text-xs dark:bg-gray-700"
          >
            {match[3]}
          </code>,
        )
      }
      lastIndex = regex.lastIndex
    }
    if (lastIndex < text.length) {
      parts.push(text.slice(lastIndex))
    }
    return parts.length === 1 ? parts[0] : <>{parts}</>
  }

  for (const line of lines) {
    if (line.trimStart().startsWith('```')) {
      if (codeBlockBuffer !== null) {
        flushCodeBlock()
      } else {
        flushList()
        codeBlockBuffer = []
      }
      continue
    }
    if (codeBlockBuffer !== null) {
      codeBlockBuffer.push(line)
      continue
    }

    const bulletMatch = line.match(/^[-*]\s+(.*)/)
    const numberMatch = line.match(/^(\d+)\.\s+(.*)/)

    if (bulletMatch) {
      if (!listBuffer || listBuffer.type !== 'ul') {
        flushList()
        listBuffer = { type: 'ul', items: [] }
      }
      listBuffer.items.push(formatInline(bulletMatch[1]))
    } else if (numberMatch) {
      if (!listBuffer || listBuffer.type !== 'ol') {
        flushList()
        listBuffer = { type: 'ol', items: [] }
      }
      listBuffer.items.push(formatInline(numberMatch[2]))
    } else {
      flushList()
      if (line.trim() === '') {
        elements.push(<div key={`br-${elements.length}`} className="h-2" />)
      } else {
        elements.push(
          <p key={`p-${elements.length}`} className="break-words">{formatInline(line)}</p>,
        )
      }
    }
  }
  flushList()
  flushCodeBlock()

  return <div className="space-y-1">{elements}</div>
}

// ---------------------------------------------------------------------------
// Typewriter hook for simulated streaming
// ---------------------------------------------------------------------------

function useTypewriter(fullText: string, enabled: boolean, speed = 12) {
  const [displayed, setDisplayed] = useState(enabled ? '' : fullText)
  const [done, setDone] = useState(!enabled)

  useEffect(() => {
    if (!enabled) {
      setDisplayed(fullText)
      setDone(true)
      return
    }
    setDisplayed('')
    setDone(false)
    let i = 0
    const timer = setInterval(() => {
      // Advance by chunks for speed — larger chunks for long responses
      const chunkSize = fullText.length > 500 ? 4 : fullText.length > 200 ? 3 : 2
      i = Math.min(i + chunkSize, fullText.length)
      setDisplayed(fullText.slice(0, i))
      if (i >= fullText.length) {
        setDone(true)
        clearInterval(timer)
      }
    }, speed)
    return () => clearInterval(timer)
  }, [fullText, enabled, speed])

  return { displayed, done }
}

function StreamingMessage({ content, onDone }: { content: string; onDone: () => void }) {
  const { displayed, done } = useTypewriter(content, true)

  useEffect(() => {
    if (done) onDone()
  }, [done, onDone])

  return (
    <>
      {renderFormattedContent(displayed)}
      {!done && <span className="inline-block h-4 w-1 animate-pulse bg-violet-500" />}
    </>
  )
}

// ---------------------------------------------------------------------------
// Thinking indicator with tool-specific steps
// ---------------------------------------------------------------------------

const toolSteps = [
  { icon: Search, text: 'Analyzing your question...', color: 'text-blue-500' },
  { icon: Database, text: 'Querying cluster data...', color: 'text-cyan-500' },
  { icon: Wrench, text: 'Calling list_clusters...', color: 'text-violet-500' },
  { icon: Shield, text: 'Checking addon health...', color: 'text-green-500' },
  { icon: Wrench, text: 'Calling get_addon_values...', color: 'text-violet-500' },
  { icon: Database, text: 'Comparing versions...', color: 'text-amber-500' },
  { icon: Wrench, text: 'Calling find_addon_deployments...', color: 'text-violet-500' },
  { icon: RefreshCw, text: 'Processing tool results...', color: 'text-blue-400' },
  { icon: Sparkles, text: 'Generating response...', color: 'text-violet-500' },
]

function ThinkingProcess() {
  const [stepIndex, setStepIndex] = useState(0)
  const [completedSteps, setCompletedSteps] = useState<number[]>([])

  useEffect(() => {
    const interval = setInterval(() => {
      setStepIndex((prev) => {
        const next = (prev + 1) % toolSteps.length
        setCompletedSteps((cs) => [...cs, prev])
        return next
      })
    }, 2200)
    return () => clearInterval(interval)
  }, [])

  const currentStep = toolSteps[stepIndex]
  const Icon = currentStep.icon
  const recentCompleted = completedSteps.slice(-2)

  return (
    <div className="flex gap-3">
      <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-violet-100 dark:bg-violet-900/40">
        <Sparkles className="h-4 w-4 animate-pulse text-violet-600 dark:text-violet-400" />
      </div>
      <div className="space-y-1 rounded-2xl border border-violet-200 bg-violet-50 px-4 py-2.5 dark:border-violet-800 dark:bg-violet-950/30">
        {/* Completed steps (faded) */}
        {recentCompleted.map((si) => {
          const step = toolSteps[si]
          const StepIcon = step.icon
          return (
            <div key={si} className="flex items-center gap-2 text-xs text-gray-400 dark:text-gray-500">
              <StepIcon className="h-3 w-3" />
              <span className="line-through">{step.text}</span>
            </div>
          )
        })}
        {/* Current step */}
        <div className="flex items-center gap-2 text-sm text-violet-700 dark:text-violet-300">
          <Icon className={`h-3.5 w-3.5 animate-spin ${currentStep.color}`} />
          {currentStep.text}
        </div>
        <div className="flex items-center gap-1">
          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-violet-400 [animation-delay:0ms]" />
          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-violet-400 [animation-delay:150ms]" />
          <span className="h-1.5 w-1.5 animate-bounce rounded-full bg-violet-400 [animation-delay:300ms]" />
        </div>
      </div>
    </div>
  )
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export function AIAssistant({ embedded = false, pageContext, initialMessage }: { embedded?: boolean; pageContext?: string; initialMessage?: string } = {}) {
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [sessionId, setSessionId] = useState(() => crypto.randomUUID())
  const [aiEnabled, setAiEnabled] = useState<boolean | null>(null)
  const [, setTick] = useState(0)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)

  // Check AI status on mount
  useEffect(() => {
    api
      .getAIStatus()
      .then((res) => setAiEnabled(res.enabled))
      .catch(() => setAiEnabled(false))
  }, [])

  // Auto-send initialMessage when AI is ready
  useEffect(() => {
    if (initialMessage && aiEnabled === true && messages.length === 0) {
      void sendMessage(initialMessage)
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initialMessage, aiEnabled])

  // Auto-scroll on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, loading])

  // Update relative timestamps every 30s
  useEffect(() => {
    const interval = setInterval(() => setTick((t) => t + 1), 30000)
    return () => clearInterval(interval)
  }, [])

  const sendMessage = useCallback(
    async (text: string) => {
      const trimmed = text.trim()
      if (!trimmed || loading) return

      const userMsg: ChatMessage = {
        id: crypto.randomUUID(),
        role: 'user',
        content: trimmed,
        timestamp: new Date(),
      }
      setMessages((prev) => [...prev, userMsg])
      setInput('')
      setLoading(true)

      try {
        const res = await api.agentChat(sessionId, trimmed, pageContext)
        const assistantMsg: ChatMessage = {
          id: crypto.randomUUID(),
          role: 'assistant',
          content: res.response,
          timestamp: new Date(),
          streaming: true,
        }
        setMessages((prev) => [...prev, assistantMsg])
      } catch (err) {
        const errorMsg: ChatMessage = {
          id: crypto.randomUUID(),
          role: 'assistant',
          content: `Sorry, I encountered an error: ${err instanceof Error ? err.message : 'Unknown error'}. Please try again.`,
          timestamp: new Date(),
        }
        setMessages((prev) => [...prev, errorMsg])
      } finally {
        setLoading(false)
        textareaRef.current?.focus()
      }
    },
    [loading, sessionId],
  )

  const markStreamingDone = useCallback((msgId: string) => {
    setMessages((prev) =>
      prev.map((m) => (m.id === msgId ? { ...m, streaming: false } : m)),
    )
  }, [])

  const handleNewConversation = useCallback(async () => {
    try {
      await api.agentReset(sessionId)
    } catch {
      // ignore reset errors
    }
    const newId = crypto.randomUUID()
    setSessionId(newId)
    setMessages([])
    setInput('')
    setLoading(false)
  }, [sessionId])

  const handleExport = useCallback(() => {
    const now = new Date()
    const dateStr = now.toISOString().slice(0, 10)
    const header = [
      'ArgoCD Addons Platform - AI Chat Export',
      `Date: ${dateStr}`,
      `Messages: ${messages.length}`,
      '=====================================',
      '',
    ].join('\n')

    const body = messages
      .map((msg) => {
        const ts = msg.timestamp.toISOString()
        const role = msg.role === 'user' ? 'User' : 'Assistant'
        return `[${ts}] ${role}:\n${msg.content}\n`
      })
      .join('\n')

    const text = header + body
    const blob = new Blob([text], { type: 'text/plain' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `aap-chat-${dateStr}.txt`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }, [messages])

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage(input)
    }
  }

  // AI not configured state
  if (aiEnabled === false) {
    return (
      <div className={`flex flex-col items-center justify-center gap-4 text-center ${embedded ? 'h-full p-6' : 'h-[calc(100vh-7rem)]'}`}>
        <Sparkles className="h-12 w-12 text-gray-400" />
        <h2 className="text-xl font-semibold text-gray-700 dark:text-gray-300">
          AI not configured
        </h2>
        <p className="max-w-md text-gray-500 dark:text-gray-400">
          Go to{' '}
          <a href="/settings" className="font-medium text-cyan-600 underline hover:text-cyan-700 dark:text-cyan-400 dark:hover:text-cyan-300">
            Settings
          </a>{' '}
          to configure an AI provider.
        </p>
      </div>
    )
  }

  return (
    <div className={`flex flex-col ${embedded ? 'h-full' : 'h-[calc(100vh-7rem)]'}`}>
      {/* Header */}
      {/* Header — hidden when embedded (floating panel has its own header) */}
      {!embedded && (
        <div className="flex items-center justify-between border-b border-gray-200 pb-4 dark:border-gray-700">
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-violet-100 dark:bg-violet-900/40">
              <Sparkles className="h-5 w-5 text-violet-600 dark:text-violet-400" />
            </div>
            <div>
              <h1 className="text-lg font-semibold text-gray-900 dark:text-gray-100">
                AI Assistant
              </h1>
              <p className="text-xs text-gray-500 dark:text-gray-400">
                Ask questions about your add-on deployments
              </p>
            </div>
          </div>
          <div className="flex items-center gap-3">
            {messages.length > 0 && (
              <span className="rounded-full bg-gray-100 px-2 py-0.5 text-[10px] text-gray-500 dark:bg-gray-800 dark:text-gray-400">
                {messages.length} messages
              </span>
            )}
            {messages.length > 0 && (
              <button
                onClick={handleExport}
                className="flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-800"
              >
                <Download className="h-3.5 w-3.5" />
                Export
              </button>
            )}
            <button
              onClick={handleNewConversation}
              className="flex items-center gap-1.5 rounded-lg border border-gray-200 px-3 py-1.5 text-xs font-medium text-gray-600 transition-colors hover:bg-gray-100 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              <RotateCcw className="h-3.5 w-3.5" />
              New
            </button>
          </div>
        </div>
      )}
      {/* Embedded mini toolbar */}
      {embedded && messages.length > 0 && (
        <div className="flex items-center justify-end gap-2 border-b border-gray-200 px-3 py-1.5 dark:border-gray-700">
          <button onClick={handleNewConversation} className="text-[10px] text-gray-500 hover:text-gray-700 dark:text-gray-400">
            <RotateCcw className="inline h-3 w-3" /> New
          </button>
        </div>
      )}

      {/* Messages area */}
      <div className="flex-1 overflow-y-auto py-4 scrollbar-thin scrollbar-track-transparent scrollbar-thumb-gray-300 dark:scrollbar-thumb-gray-600">
        {messages.length === 0 && !loading ? (
          /* Suggested prompts */
          <div className="flex h-full flex-col items-center justify-center gap-6 px-4">
            <div className="text-center">
              <Sparkles className="mx-auto mb-3 h-10 w-10 text-violet-400" />
              <h2 className="text-lg font-medium text-gray-700 dark:text-gray-300">
                How can I help you?
              </h2>
              <p className="mt-1 text-sm text-gray-500 dark:text-gray-400">
                Try one of these suggestions or ask your own question
              </p>
            </div>
            <div className="grid max-w-2xl grid-cols-1 gap-2 sm:grid-cols-2">
              {SUGGESTED_PROMPTS.map((prompt) => (
                <button
                  key={prompt}
                  onClick={() => sendMessage(prompt)}
                  className="rounded-lg border border-gray-200 px-4 py-3 text-left text-sm text-gray-600 transition-colors hover:border-cyan-300 hover:bg-cyan-50 dark:border-gray-600 dark:text-gray-300 dark:hover:border-cyan-700 dark:hover:bg-cyan-900/20"
                >
                  {prompt}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <div className="mx-auto max-w-3xl space-y-4 px-4">
            {messages.map((msg) => (
              <div
                key={msg.id}
                className={`flex gap-3 ${msg.role === 'user' ? 'flex-row-reverse' : 'flex-row'}`}
              >
                {/* Avatar */}
                <div
                  className={`flex h-8 w-8 shrink-0 items-center justify-center rounded-full ${
                    msg.role === 'user'
                      ? 'bg-cyan-100 dark:bg-cyan-900/40'
                      : 'bg-violet-100 dark:bg-violet-900/40'
                  }`}
                >
                  {msg.role === 'user' ? (
                    <User className="h-4 w-4 text-cyan-600 dark:text-cyan-400" />
                  ) : (
                    <Sparkles className="h-4 w-4 text-violet-600 dark:text-violet-400" />
                  )}
                </div>

                {/* Bubble */}
                <div
                  className={`max-w-[75%] overflow-hidden rounded-2xl px-4 py-2.5 text-sm ${
                    msg.role === 'user'
                      ? 'bg-cyan-600 text-white dark:bg-cyan-700'
                      : 'border border-gray-200 bg-gray-100 text-gray-800 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200'
                  }`}
                >
                  {msg.role === 'assistant' ? (
                    msg.streaming ? (
                      <StreamingMessage
                        content={msg.content}
                        onDone={() => markStreamingDone(msg.id)}
                      />
                    ) : (
                      renderFormattedContent(msg.content)
                    )
                  ) : (
                    <p className="whitespace-pre-wrap">{msg.content}</p>
                  )}
                  <p
                    className={`mt-1.5 text-[10px] ${
                      msg.role === 'user'
                        ? 'text-cyan-200'
                        : 'text-gray-400 dark:text-gray-500'
                    }`}
                  >
                    {formatRelativeTime(msg.timestamp)}
                  </p>
                </div>
              </div>
            ))}

            {/* Typing indicator */}
            {loading && <ThinkingProcess />}

            <div ref={messagesEndRef} />
          </div>
        )}
      </div>

      {/* Input area */}
      <div className="border-t border-gray-200 bg-white px-4 py-3 dark:border-gray-700 dark:bg-gray-900">
        <div className="mx-auto flex max-w-3xl items-end gap-3">
          <textarea
            ref={textareaRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Ask about your addons, clusters, or upgrades..."
            disabled={loading || aiEnabled === null}
            rows={1}
            className="max-h-32 min-h-[2.5rem] flex-1 resize-none rounded-xl border border-gray-300 bg-gray-50 px-4 py-2.5 text-sm text-gray-900 placeholder-gray-400 focus:border-cyan-500 focus:outline-none focus:ring-1 focus:ring-cyan-500 disabled:opacity-50 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-100 dark:placeholder-gray-500 dark:focus:border-cyan-500"
          />
          <button
            onClick={() => sendMessage(input)}
            disabled={!input.trim() || loading || aiEnabled === null}
            className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-cyan-600 text-white transition-colors hover:bg-cyan-700 disabled:cursor-not-allowed disabled:opacity-40 dark:bg-cyan-700 dark:hover:bg-cyan-600"
            aria-label="Send message"
          >
            <Send className="h-4 w-4" />
          </button>
        </div>
      </div>
    </div>
  )
}
