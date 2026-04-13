import { useState, useEffect, useCallback } from 'react'
import {
  Loader2,
  Pencil,
  X,
  Sparkles,
  CheckCircle,
  XCircle,
} from 'lucide-react'
import { api } from '@/services/api'
import type { AIConfigResponse, AIProviderInfo } from '@/services/models'

const labelCls = 'block text-sm font-medium text-[#0a3a5a] dark:text-gray-300'
const inputCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm placeholder:text-[#3a6a8a] focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100 dark:placeholder:text-[#2a5a7a]'
const selectCls =
  'mt-1 block w-full rounded-lg border border-[#5a9dd0] bg-[#f0f7ff] px-3 py-2 text-sm text-[#0a2a4a] shadow-sm focus:border-teal-500 focus:outline-none focus:ring-1 focus:ring-teal-500 dark:border-gray-600 dark:bg-gray-700 dark:text-gray-100'

export function AIConfigSection() {
  const [aiConfig, setAiConfig] = useState<AIConfigResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [testResult, setTestResult] = useState<string | null>(null)
  const [testing, setTesting] = useState(false)
  const [showForm, setShowForm] = useState(false)
  const [formProvider, setFormProvider] = useState('gemini')
  const [formApiKey, setFormApiKey] = useState('')
  const [formModel, setFormModel] = useState('')
  const [formBaseURL, setFormBaseURL] = useState('')
  const [formOllamaURL, setFormOllamaURL] = useState('http://localhost:11434')
  const [formTestStatus, setFormTestStatus] = useState<'idle' | 'testing' | 'ok' | 'error'>('idle')
  const [formTestMsg, setFormTestMsg] = useState('')
  const [saving, setSaving] = useState(false)

  const providerModels: Record<string, string[]> = {
    gemini: ['gemini-2.5-flash', 'gemini-2.5-pro', 'gemini-2.0-flash', 'gemini-1.5-pro'],
    claude: ['claude-sonnet-4-20250514', 'claude-haiku-4-5-20251001', 'claude-opus-4-20250514'],
    openai: ['gpt-4o', 'gpt-4o-mini', 'gpt-4-turbo', 'o3-mini'],
    ollama: ['llama3.2', 'llama3.1:8b', 'qwen2.5', 'mistral', 'llama3.1:70b'],
    'custom-openai': [],
  }
  const defaultModels: Record<string, string> = {
    gemini: 'gemini-2.5-flash',
    claude: 'claude-sonnet-4-20250514',
    openai: 'gpt-4o',
    ollama: 'llama3.2',
    'custom-openai': '',
  }

  const fetchConfig = useCallback(() => {
    setLoading(true)
    api.getAIConfig()
      .then(setAiConfig)
      .catch(() => setAiConfig(null))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { fetchConfig() }, [fetchConfig])

  const isEnabled = aiConfig?.current_provider && aiConfig.current_provider !== 'none' && aiConfig.current_provider !== ''
  const activeProvider = aiConfig?.available_providers.find((p: AIProviderInfo) => p.id === aiConfig.current_provider)

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      const res = await api.testAI()
      setTestResult(res.status === 'ok' ? 'AI is responding correctly' : 'AI returned unexpected response')
    } catch (err) {
      setTestResult(err instanceof Error ? err.message : 'Connection failed')
    } finally { setTesting(false) }
  }

  const handleFormTest = async () => {
    setFormTestStatus('testing')
    setFormTestMsg('')
    try {
      const res = await api.testAIConfig({
        provider: formProvider,
        api_key: formApiKey || undefined,
        model: formModel || defaultModels[formProvider] || undefined,
        base_url: formBaseURL || undefined,
        ollama_url: formProvider === 'ollama' ? formOllamaURL : undefined,
      })
      if (res.status === 'ok') {
        setFormTestStatus('ok')
        setFormTestMsg(res.response || 'Connected')
      } else {
        setFormTestStatus('error')
        setFormTestMsg(res.message || 'Test failed')
      }
    } catch (err) {
      setFormTestStatus('error')
      setFormTestMsg(err instanceof Error ? err.message : 'Test failed')
    }
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      await api.saveAIConfig({
        provider: formProvider,
        api_key: formApiKey || undefined,
        model: formModel || defaultModels[formProvider] || undefined,
        base_url: formBaseURL || undefined,
        ollama_url: formProvider === 'ollama' ? formOllamaURL : undefined,
      })
      setShowForm(false)
      setFormTestStatus('idle')
      fetchConfig()
    } catch (err) {
      setFormTestStatus('error')
      setFormTestMsg(err instanceof Error ? err.message : 'Save failed')
    } finally { setSaving(false) }
  }

  const handleDisable = async () => {
    try {
      await api.saveAIConfig({ provider: 'none' })
      fetchConfig()
    } catch { /* ignore */ }
  }

  const openEditForm = () => {
    const cfg = aiConfig
    if (cfg && isEnabled && activeProvider) {
      setFormProvider(activeProvider.id)
      setFormModel(activeProvider.model || '')
    }
    setFormApiKey('')
    setFormTestStatus('idle')
    setFormTestMsg('')
    setShowForm(true)
  }

  return (
    <div className="rounded-xl ring-2 ring-[#6aade0] bg-[#f0f7ff] p-6 shadow-sm dark:border-gray-700 dark:bg-gray-800">
      {/* Header */}
      <div className="flex items-start justify-between">
        <div className="flex items-center gap-3">
          <div className={`flex h-10 w-10 items-center justify-center rounded-lg ${isEnabled ? 'bg-purple-100 dark:bg-purple-900/30' : 'bg-[#d6eeff] dark:bg-gray-700'}`}>
            <Sparkles className={`h-5 w-5 ${isEnabled ? 'text-purple-600 dark:text-purple-400' : 'text-[#3a6a8a]'}`} />
          </div>
          <div>
            <h4 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">
              AI Analysis
              {loading ? '' : isEnabled ? (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-green-100 px-2 py-0.5 text-xs font-medium text-green-700 dark:bg-green-900/30 dark:text-green-400">
                  <span className="inline-block h-1.5 w-1.5 rounded-full bg-green-500" />
                  Active
                </span>
              ) : (
                <span className="ml-2 inline-flex items-center gap-1 rounded-full bg-[#d6eeff] px-2 py-0.5 text-xs font-medium text-[#2a5a7a] dark:bg-gray-700 dark:text-gray-400">
                  Not Configured
                </span>
              )}
            </h4>
            <p className="mt-0.5 text-xs text-[#2a5a7a] dark:text-gray-400">
              {isEnabled && activeProvider
                ? `Using ${activeProvider.name}${activeProvider.model ? ` — ${activeProvider.model}` : ''}`
                : 'Configure an AI provider for upgrade analysis and migration assistance'}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-2">
          {isEnabled && (
            <>
              <button onClick={handleTest} disabled={testing}
                className="rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
                {testing ? 'Testing...' : 'Test'}
              </button>
              <button onClick={handleDisable}
                className="rounded-lg border border-red-300 px-3 py-1.5 text-xs font-medium text-red-600 hover:bg-red-50 dark:border-red-700 dark:text-red-400 dark:hover:bg-red-900/20">
                Disable
              </button>
            </>
          )}
          <button onClick={openEditForm}
            className="inline-flex items-center gap-1 rounded-lg bg-purple-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-purple-700 dark:bg-purple-700 dark:hover:bg-purple-600">
            <Pencil className="h-3 w-3" />
            {isEnabled ? 'Edit' : 'Configure'}
          </button>
        </div>
      </div>

      {testResult && (
        <div className={`mt-3 flex items-center gap-2 rounded-lg px-3 py-2 text-xs ${
          testResult.includes('correctly') ? 'bg-green-50 text-green-700 dark:bg-green-900/20 dark:text-green-400' : 'bg-red-50 text-red-700 dark:bg-red-900/20 dark:text-red-400'
        }`}>
          {testResult.includes('correctly') ? <CheckCircle className="h-3.5 w-3.5" /> : <XCircle className="h-3.5 w-3.5" />}
          {testResult}
        </div>
      )}

      {/* Configure Form */}
      {showForm && (
        <div className="mt-4 rounded-lg border border-purple-200 bg-purple-50/50 p-4 dark:border-purple-800 dark:bg-purple-950/20">
          <div className="mb-3 flex items-center justify-between">
            <h5 className="text-sm font-semibold text-[#0a2a4a] dark:text-gray-100">Configure AI Provider</h5>
            <button onClick={() => setShowForm(false)} className="text-[#3a6a8a] hover:text-[#1a4a6a] dark:hover:text-gray-200">
              <X className="h-4 w-4" />
            </button>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className={labelCls}>Provider</label>
              <select className={selectCls} value={formProvider} onChange={(e) => {
                setFormProvider(e.target.value)
                setFormModel(defaultModels[e.target.value] || '')
                setFormTestStatus('idle')
              }}>
                <option value="gemini">Gemini (Google)</option>
                <option value="claude">Claude (Anthropic)</option>
                <option value="openai">OpenAI</option>
                <option value="ollama">Ollama (Local)</option>
                <option value="custom-openai">Custom OpenAI-compatible</option>
              </select>
            </div>
            {formProvider !== 'ollama' && (
              <div>
                <label className={labelCls}>API Key</label>
                <input className={inputCls} type="password" value={formApiKey} onChange={(e) => { setFormApiKey(e.target.value); setFormTestStatus('idle') }}
                  placeholder={formProvider === 'gemini' ? 'AIzaSy...' : formProvider === 'claude' ? 'sk-ant-...' : 'sk-...'} />
              </div>
            )}
            <div>
              <label className={labelCls}>Model</label>
              {providerModels[formProvider]?.length > 0 ? (
                <select className={selectCls} value={formModel} onChange={(e) => { setFormModel(e.target.value); setFormTestStatus('idle') }}>
                  {providerModels[formProvider].map(m => (
                    <option key={m} value={m}>{m}</option>
                  ))}
                </select>
              ) : (
                <input className={inputCls} value={formModel} onChange={(e) => { setFormModel(e.target.value); setFormTestStatus('idle') }}
                  placeholder="model name" />
              )}
            </div>
            {formProvider === 'ollama' && (
              <div>
                <label className={labelCls}>Ollama URL</label>
                <input className={inputCls} value={formOllamaURL} onChange={(e) => { setFormOllamaURL(e.target.value); setFormTestStatus('idle') }}
                  placeholder="http://localhost:11434" />
              </div>
            )}
            {formProvider === 'custom-openai' && (
              <div>
                <label className={labelCls}>Base URL</label>
                <input className={inputCls} value={formBaseURL} onChange={(e) => { setFormBaseURL(e.target.value); setFormTestStatus('idle') }}
                  placeholder="https://your-gateway.example.com/api" />
              </div>
            )}
          </div>
          <div className="mt-3 flex items-center gap-3">
            <button onClick={handleFormTest} disabled={formTestStatus === 'testing'}
              className="inline-flex items-center gap-1.5 rounded-lg border border-[#5a9dd0] px-3 py-1.5 text-xs font-medium text-[#0a3a5a] hover:bg-[#d6eeff] disabled:opacity-50 dark:border-gray-600 dark:text-gray-300 dark:hover:bg-gray-700">
              {formTestStatus === 'testing' ? <Loader2 className="h-3 w-3 animate-spin" /> : <Sparkles className="h-3 w-3" />}
              Test AI
            </button>
            {formTestStatus === 'ok' && <span className="flex items-center gap-1 text-xs text-green-600 dark:text-green-400"><CheckCircle className="h-3.5 w-3.5" /> Connected</span>}
            {formTestStatus === 'error' && <span className="flex items-center gap-1 text-xs text-red-600 dark:text-red-400"><XCircle className="h-3.5 w-3.5" /> {formTestMsg}</span>}
          </div>
          <div className="mt-4 flex items-center gap-3">
            <button onClick={handleSave} disabled={saving || formTestStatus !== 'ok'}
              title={formTestStatus !== 'ok' ? 'Test the connection first' : undefined}
              className="inline-flex items-center gap-1.5 rounded-lg bg-purple-600 px-4 py-2 text-sm font-medium text-white shadow-sm hover:bg-purple-700 disabled:opacity-50 dark:bg-purple-700 dark:hover:bg-purple-600">
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              Save
            </button>
            <button onClick={() => setShowForm(false)} className="rounded-lg px-4 py-2 text-sm font-medium text-[#1a4a6a] hover:text-[#0a3a5a] dark:text-gray-400 dark:hover:text-gray-200">
              Cancel
            </button>
          </div>
        </div>
      )}
    </div>
  )
}
