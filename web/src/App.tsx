import { useState, useRef, useEffect, useCallback } from 'react'
import './App.css'
import { getApertureKey, setApertureKey, getAdminKey, setAdminKey, adminHeaders } from './auth'

const API_URL = import.meta.env.VITE_APERTURE_URL || 'http://localhost:8080'
const DEFAULT_MODEL = 'gpt-4o-mini'
const MODEL_STORAGE_KEY = 'aperture-model'

const MODELS = [
  { id: 'gpt-4o', label: 'GPT-4o' },
  { id: 'gpt-4o-mini', label: 'GPT-4o Mini' },
  { id: 'gpt-4-turbo', label: 'GPT-4 Turbo' },
  { id: 'gpt-4', label: 'GPT-4' },
  { id: 'gpt-3.5-turbo', label: 'GPT-3.5 Turbo' },
  { id: 'o1', label: 'o1' },
  { id: 'o1-mini', label: 'o1-mini' },
]

interface Message {
  id: string
  role: 'user' | 'assistant'
  content: string
}

function extractErrorMessage(value: unknown, fallback: string): string {
  if (typeof value === 'string' && value.trim()) return value
  if (value && typeof value === 'object') {
    const obj = value as { message?: unknown; error?: unknown }
    if (typeof obj.message === 'string' && obj.message.trim()) return obj.message
    if (typeof obj.error === 'string' && obj.error.trim()) return obj.error
  }
  return fallback
}

function App() {
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [showAdmin, setShowAdmin] = useState(false)
  const [model, setModel] = useState(() =>
    localStorage.getItem(MODEL_STORAGE_KEY) || DEFAULT_MODEL
  )
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const sendMessage = async () => {
    const text = input.trim()
    if (!text || isLoading) return

    const apertureKey = getApertureKey()
    if (!apertureKey) {
      setError('Set your Aperture API key in Settings (the server prints it at startup)')
      return
    }

    setError(null)
    setInput('')
    const userMsg: Message = { id: crypto.randomUUID(), role: 'user', content: text }
    setMessages((m) => [...m, userMsg])
    setIsLoading(true)

    const assistantId = crypto.randomUUID()
    setMessages((m) => [...m, { id: assistantId, role: 'assistant', content: '' }])

    abortRef.current = new AbortController()

    try {
      const response = await fetch(`${API_URL}/v1/chat/completions`, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${apertureKey}`,
        },
        body: JSON.stringify({
          model,
          messages: [...messages, userMsg].map((m) => ({ role: m.role, content: m.content })),
          stream: true,
        }),
        signal: abortRef.current.signal,
      })

      if (!response.ok) {
        const err = await response.json().catch(() => null)
        throw new Error(extractErrorMessage(err, `HTTP ${response.status}`))
      }

      const reader = response.body?.getReader()
      const decoder = new TextDecoder()
      let content = ''

      if (reader) {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          const chunk = decoder.decode(value, { stream: true })
          const lines = chunk.split('\n').filter((l) => l.startsWith('data: '))
          for (const line of lines) {
            const data = line.slice(6)
            if (data === '[DONE]') continue
            try {
              const parsed = JSON.parse(data) as { choices?: Array<{ delta?: { content?: string } }> }
              const delta = parsed.choices?.[0]?.delta?.content
              if (delta) {
                content += delta
                setMessages((m) =>
                  m.map((msg) =>
                    msg.id === assistantId ? { ...msg, content } : msg
                  )
                )
              }
            } catch {
              // skip invalid json
            }
          }
        }
      }
    } catch (err) {
      if ((err as Error).name === 'AbortError') return
      setError((err as Error).message)
      setMessages((m) => m.filter((msg) => msg.id !== assistantId))
    } finally {
      setIsLoading(false)
      abortRef.current = null
    }
  }

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  return (
    <div className="app">
      <header className="header">
        <h1 className="logo">Aperture</h1>
        <button
          type="button"
          className="settings-btn"
          onClick={() => setShowAdmin(true)}
          title="Settings"
        >
          ⚙
        </button>
      </header>

      <main className="main">
        {messages.length === 0 ? (
          <div className="empty">
            <p className="empty-title">Start a conversation</p>
            <p className="empty-sub">Configure your API key in the settings panel</p>
          </div>
        ) : (
          <div className="messages">
            {messages.map((msg) => (
              <div key={msg.id} className={`message message--${msg.role}`}>
                <div className="message-content">{msg.content || '\u00A0'}</div>
              </div>
            ))}
            <div ref={messagesEndRef} />
          </div>
        )}
      </main>

      {error && (
        <div className="error-banner">
          {error}
          <button type="button" onClick={() => setError(null)} className="error-close">×</button>
        </div>
      )}

      <footer className="footer">
        <div className="input-wrap">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={handleKeyDown}
            placeholder="Message..."
            rows={1}
            disabled={isLoading}
            className="input"
          />
          <button
            type="button"
            onClick={sendMessage}
            disabled={!input.trim() || isLoading}
            className="send-btn"
            aria-label="Send"
          >
            →
          </button>
        </div>
      </footer>

      {showAdmin && (
        <AdminPanel
          apiUrl={API_URL}
          model={model}
          onModelChange={(m) => {
            setModel(m)
            localStorage.setItem(MODEL_STORAGE_KEY, m)
          }}
          onClose={() => setShowAdmin(false)}
        />
      )}
    </div>
  )
}

function AdminPanel({
  apiUrl,
  model,
  onModelChange,
  onClose,
}: {
  apiUrl: string
  model: string
  onModelChange: (m: string) => void
  onClose: () => void
}) {
  const [openaiKey, setOpenaiKey] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [configured, setConfigured] = useState(false)
  const [maskedKey, setMaskedKey] = useState('')
  const [saving, setSaving] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [status, setStatus] = useState<string | null>(null)
  const [apertureKey, setApertureKeyState] = useState(getApertureKey)
  const [adminKey, setAdminKeyState] = useState(getAdminKey)

  const saveApertureKey = (v: string) => {
    setApertureKeyState(v)
    setApertureKey(v)
  }
  const saveAdminKey = (v: string) => {
    setAdminKeyState(v)
    setAdminKey(v)
  }

  const fetchConfig = useCallback(() => {
    fetch(`${apiUrl}/admin/config`, { headers: adminHeaders() })
      .then((r) => {
        if (r.status === 401) throw new Error('unauthorized')
        return r.json()
      })
      .then((d: { configured?: boolean; masked_key?: string }) => {
        setConfigured(d.configured ?? false)
        setMaskedKey(d.masked_key ?? '')
      })
      .catch(() => { setConfigured(false); setMaskedKey('') })
  }, [apiUrl])

  useEffect(() => {
    fetchConfig()
  }, [fetchConfig])

  useEffect(() => {
    const onPaste = (e: ClipboardEvent) => {
      // Let pastes into inputs (aperture/admin key fields) behave normally.
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return
      const text = e.clipboardData?.getData('text/plain')
      if (text?.trim()) {
        e.preventDefault()
        setOpenaiKey(text.trim())
        setStatus(null)
      }
    }
    window.addEventListener('paste', onPaste, true)
    return () => window.removeEventListener('paste', onPaste, true)
  }, [])

  const handlePaste = async () => {
    try {
      const text = await navigator.clipboard.readText()
      if (text.trim()) {
        setOpenaiKey(text.trim())
        setStatus(null)
      } else {
        setStatus('Clipboard is empty')
      }
    } catch {
      setStatus('Clipboard unavailable. Use "Load from file" below.')
    }
  }

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      const text = (reader.result as string)?.trim()
      if (text) {
        setOpenaiKey(text)
        setStatus(null)
      }
      e.target.value = ''
    }
    reader.readAsText(file)
  }

  const handleDelete = async () => {
    setDeleting(true)
    setStatus(null)
    try {
      const res = await fetch(`${apiUrl}/admin/config`, { method: 'DELETE', headers: adminHeaders() })
      const data = (await res.json().catch(() => ({}))) as { error?: string; ok?: boolean }
      if (!res.ok) {
        setStatus(extractErrorMessage(data, `Error ${res.status}`))
        return
      }
      setConfigured(false)
      setMaskedKey('')
      setOpenaiKey('')
      setStatus('Key deleted')
      fetchConfig()
    } catch (err) {
      setStatus((err as Error).message)
    } finally {
      setDeleting(false)
    }
  }

  const handleSave = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    setStatus(null)
    try {
      const res = await fetch(`${apiUrl}/admin/config`, {
        method: 'POST',
        headers: adminHeaders({ 'Content-Type': 'application/json' }),
        body: JSON.stringify({ openai_api_key: openaiKey }),
      })
      const data = (await res.json().catch(() => ({}))) as { error?: string; ok?: boolean }
      if (!res.ok) {
        setStatus(extractErrorMessage(data, `Error ${res.status}`))
        return
      }
      setConfigured(true)
      setOpenaiKey('')
      setStatus('Key saved')
      fetchConfig()
    } catch (err) {
      setStatus((err as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="modal-overlay" onClick={onClose}>
      <div className="modal" onClick={(e) => e.stopPropagation()}>
        <div className="modal-header">
          <h2>Settings</h2>
          <button type="button" className="modal-close" onClick={onClose}>×</button>
        </div>
        <div className="modal-body">
          <div className="modal-field">
            <label className="modal-label">Model</label>
            <select
              className="modal-select"
              value={model}
              onChange={(e) => onModelChange(e.target.value)}
            >
              {MODELS.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.label}
                </option>
              ))}
              {!MODELS.some((m) => m.id === model) && model && (
                <option value={model}>{model}</option>
              )}
            </select>
          </div>
          <div className="modal-field">
            <label className="modal-label">Aperture API key (used by this chat)</label>
            <input
              type="password"
              placeholder="ap-... (printed in server log at startup)"
              value={apertureKey}
              onChange={(e) => saveApertureKey(e.target.value)}
              className="modal-input"
              autoComplete="off"
            />
          </div>
          <div className="modal-field">
            <label className="modal-label">Admin API key (for settings & stats)</label>
            <input
              type="password"
              placeholder="admin-... (printed in server log at startup)"
              value={adminKey}
              onChange={(e) => { saveAdminKey(e.target.value) }}
              onBlur={fetchConfig}
              className="modal-input"
              autoComplete="off"
            />
          </div>
          <p className="modal-desc">
            Clipboard paste may not work in all browsers. Use "Load from file" instead.
          </p>
          {configured ? (
            <>
            <div className="modal-key-display">
              <span className="modal-key-masked">✓ {maskedKey}</span>
              <button
                type="button"
                onClick={handleDelete}
                disabled={deleting}
                className="modal-delete-btn"
              >
                {deleting ? '...' : 'Delete'}
              </button>
            </div>
            <p className="modal-hint">Delete the key to add a new one</p>
            </>
          ) : (
          <form onSubmit={handleSave} className="modal-key-form">
            <div className="modal-input-wrap">
              <div className="modal-input-with-toggle">
                <input
                  type={showKey ? 'text' : 'password'}
                  placeholder="sk-proj-..."
                  value={openaiKey}
                  onChange={(e) => setOpenaiKey(e.target.value)}
                  className="modal-input"
                  autoComplete="off"
                />
                <button
                  type="button"
                  onClick={() => setShowKey(!showKey)}
                  className="modal-input-toggle"
                  title={showKey ? 'Hide' : 'Show'}
                >
                  {showKey ? '🙈' : '👁'}
                </button>
              </div>
              <button
                type="button"
                onClick={handlePaste}
                className="modal-paste-btn"
              >
                Paste
              </button>
            </div>
            <div className="modal-file-wrap">
              <label className="modal-file-label">
                <input
                  type="file"
                  accept=".txt,.env"
                  onChange={handleFileSelect}
                  className="modal-file-input"
                />
                Load from file
              </label>
              <span className="modal-file-hint">Create a key.txt file with the key inside</span>
            </div>
            <button type="submit" disabled={saving || !openaiKey.trim()} className="modal-btn">
              {saving ? 'Saving...' : 'Save'}
            </button>
          </form>
          )}
          {status && <p className="modal-status">{status}</p>}
        </div>
      </div>
    </div>
  )
}

export default App
