import { useState, useRef, useEffect } from 'react'
import './App.css'
import { getMutegateKey, readSaved, setMutegateKey } from './auth'
import { API_URL } from './api'
const DEFAULT_MODEL = 'gpt-4o-mini'
const MODEL_STORAGE_KEY = 'mutegate-model'

interface ListedModel {
  id: string
  owned_by: string
  display_name?: string
}

// The playground talks chat completions, so models that do something else —
// embeddings, speech, images, moderation — are left out of its list. The
// gateway's /v1/models itself lists everything; this is only what makes sense
// to chat with here. Providers do not say what a model can do, so this goes
// by name, and was checked against OpenAI's live list.
const NOT_CHAT = /embed|tts|whisper|dall-e|davinci|babbage|moderation|image|realtime|audio|transcri|computer-use|sora/i
// OpenAI's own names for models chat completions refuses: the Responses-only
// ones, the old completions-only "instruct" and the live-voice family. Only
// OpenAI's: elsewhere "-instruct" is precisely the chat-tuned model.
const OPENAI_NOT_CHAT = /-pro(-|$)|codex|deep-research|instruct|\blive\b/i

function chatModel(m: ListedModel): boolean {
  if (NOT_CHAT.test(m.id)) return false
  return !(m.owned_by === 'openai' && OPENAI_NOT_CHAT.test(m.id))
}

type ModelList =
  | { state: 'no-key' }
  | { state: 'loading' }
  | { state: 'ready'; models: ListedModel[]; unavailable: { provider: string; error: string }[] }
  | { state: 'error'; message: string }

/**
 * The models this Mutegate key can use, asked of the gateway, which asks the
 * providers. Waits for typing to stop before asking with a new key.
 */
function useModels(mutegateKey: string): ModelList {
  const [list, setList] = useState<ModelList>({ state: 'loading' })
  useEffect(() => {
    const key = mutegateKey.trim()
    let live = true
    const timer = setTimeout(
      () => {
        if (!key) {
          setList({ state: 'no-key' })
          return
        }
        setList({ state: 'loading' })
        fetch(`${API_URL}/v1/models`, { headers: { Authorization: `Bearer ${key}` } })
          .then(async (r) => {
            const body = (await r.json().catch(() => ({}))) as {
              data?: ListedModel[]
              unavailable?: { provider: string; error: string }[]
              error?: unknown
            }
            if (!live) return
            if (!r.ok && !body.data?.length) {
              setList({ state: 'error', message: extractErrorMessage(body, `HTTP ${r.status}`) })
              return
            }
            setList({
              state: 'ready',
              models: (body.data ?? []).filter(chatModel),
              unavailable: body.unavailable ?? [],
            })
          })
          .catch((e) => live && setList({ state: 'error', message: (e as Error).message }))
      },
      key ? 400 : 0,
    )
    return () => {
      live = false
      clearTimeout(timer)
    }
  }, [mutegateKey])
  return list
}

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
    readSaved(MODEL_STORAGE_KEY, 'aperture-model') || DEFAULT_MODEL
  )
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const abortRef = useRef<AbortController | null>(null)

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages])

  const sendMessage = async () => {
    const text = input.trim()
    if (!text || isLoading) return

    const mutegateKey = getMutegateKey()
    if (!mutegateKey) {
      setError('Set your Mutegate API key in Settings (the server prints it at startup)')
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
          Authorization: `Bearer ${mutegateKey}`,
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
        <h1 className="logo">Mutegate</h1>
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

/**
 * The playground's own settings: which model to talk to and which Mutegate key
 * to talk with. Provider keys are not here — they belong to the organization
 * and live under Settings → Providers.
 */
function AdminPanel({
  model,
  onModelChange,
  onClose,
}: {
  model: string
  onModelChange: (m: string) => void
  onClose: () => void
}) {
  const [mutegateKey, setMutegateKeyState] = useState(getMutegateKey)
  const models = useModels(mutegateKey)
  const listed = models.state === 'ready' ? models.models : []
  const providers = [...new Set(listed.map((m) => m.owned_by))]

  const saveMutegateKey = (v: string) => {
    setMutegateKeyState(v)
    setMutegateKey(v)
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
              {providers.map((p) => (
                <optgroup key={p} label={p}>
                  {listed
                    .filter((m) => m.owned_by === p)
                    .map((m) => (
                      <option key={m.id} value={m.id}>
                        {m.display_name || m.id}
                      </option>
                    ))}
                </optgroup>
              ))}
              {!listed.some((m) => m.id === model) && model && (
                <option value={model}>{model}</option>
              )}
            </select>
            {models.state === 'no-key' && (
              <p className="modal-hint">Enter the Mutegate API key below to list the models it can use.</p>
            )}
            {models.state === 'loading' && <p className="modal-hint">Asking the providers for their models…</p>}
            {models.state === 'error' && <p className="modal-status">Could not list models: {models.message}</p>}
            {models.state === 'ready' &&
              models.unavailable.map((u) => (
                <p key={u.provider} className="modal-status">
                  {u.provider}: {u.error}
                </p>
              ))}
          </div>
          <div className="modal-field">
            <label className="modal-label">Mutegate API key (used by this chat)</label>
            <input
              type="password"
              placeholder="ap-... (from Settings → API keys)"
              value={mutegateKey}
              onChange={(e) => saveMutegateKey(e.target.value)}
              className="modal-input"
              autoComplete="off"
            />
          </div>
          <p className="modal-hint">
            Provider keys belong to the organization and are set under Settings → Providers.
          </p>
        </div>
      </div>
    </div>
  )
}

export default App
