// What a visitor who is not signed in sees. It says what Aperture is, shows
// the one thing it does in a single picture, and gets out of the way: a
// stranger can sign in, and sign up where the operator has opened registration.
import { Link } from '../router'
import { useSignInOptions } from './oauth'
import { Logo, ActionBadge, ProviderBadge } from '../console/ui'
import { card, mono } from '../console/styles'
import type { Theme } from '../theme'

const features = [
  {
    title: 'Block or redact before it leaves',
    body: 'Cloud keys, tokens, private keys, cards, phones, emails and your own patterns — caught in the prompt, the system prompt, tool calls and tool results, not just the visible message.',
  },
  {
    title: 'An incident feed without the incident',
    body: 'Who sent what, from which agent, to which model. Samples are masked before they are stored: the feed never becomes the leak it is recording.',
  },
  {
    title: 'Find out before you enforce',
    body: 'Run in alert mode for a week, then read the report: what block would have stopped, per rule and per key. Flip the switch when the answer is boring.',
  },
  {
    title: 'Budgets that stop a looping agent',
    body: 'Daily spend and request-rate ceilings per key. An agent stuck in a loop gets a 429 instead of your monthly budget.',
  },
  {
    title: 'Speaks what your agents speak',
    body: 'OpenAI Chat Completions and Responses, the native Anthropic Messages API, streaming included. Point the agent at a new base URL; nothing else changes.',
  },
  {
    title: 'Yours, on your machine',
    body: 'A single Go binary and PostgreSQL. Organizations, roles, invitations and service tokens for CI — and nothing phones home.',
  },
]

export function Landing({ theme, toggleTheme }: { theme: Theme; toggleTheme: () => void }) {
  const { registration } = useSignInOptions()
  return (
    <div className="ap-root" data-ap-theme={theme}>
      <header className="ap-landing-bar">
        <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
          <Logo />
          <span className="ap-display" style={{ fontSize: 18 }}>Aperture</span>
        </div>
        <nav style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
          <a href="https://github.com/danilovid/aperture" className="ap-ghost-btn" style={{ padding: '7px 11px', borderRadius: 7, fontSize: 13.5, color: 'var(--muted)' }}>
            GitHub
          </a>
          <button onClick={toggleTheme} className="ap-ghost-btn" aria-label="Switch theme" style={{ background: 'none', border: 'none', padding: '7px 10px', borderRadius: 7, fontSize: 14, color: 'var(--muted)', cursor: 'pointer' }}>
            {theme === 'dark' ? '☀' : '☾'}
          </button>
          {registration ? (
            <>
              <Link to="/login" className="ap-ghost-btn" style={{ padding: '7px 11px', borderRadius: 7, fontSize: 13.5, color: 'var(--text)' }}>
                Sign in
              </Link>
              <Link to="/signup" className="ap-accent-btn" style={{ background: 'var(--accent)', color: 'var(--on-accent)', padding: '8px 16px', borderRadius: 7, fontSize: 13.5, fontWeight: 600 }}>
                Sign up
              </Link>
            </>
          ) : (
            <Link to="/login" className="ap-accent-btn" style={{ background: 'var(--accent)', color: 'var(--on-accent)', padding: '8px 16px', borderRadius: 7, fontSize: 13.5, fontWeight: 600 }}>
              Sign in
            </Link>
          )}
        </nav>
      </header>

      <main className="ap-landing">
        <section className="ap-landing-hero">
          <div>
            <div style={{ ...mono, display: 'inline-flex', alignItems: 'center', gap: 8, fontSize: 12, color: 'var(--muted)', border: '1px solid var(--border2)', borderRadius: 99, padding: '5px 12px', marginBottom: 18 }}>
              <span style={{ width: 6, height: 6, borderRadius: '50%', background: 'var(--accent)' }} />
              open source · self-hosted · DLP for AI agents
            </div>
            <h1 className="ap-display" style={{ fontSize: 'clamp(34px, 4.6vw, 54px)', lineHeight: 1.04, margin: 0, fontWeight: 700, letterSpacing: '-1.4px' }}>
              Your agents talk to the cloud.
              <br />
              <span style={{ color: 'var(--muted)' }}>Know what they say.</span>
            </h1>
            <p style={{ fontSize: 16, color: 'var(--muted)', maxWidth: 520, margin: '20px 0 28px' }}>
              Aperture sits between your agents and the model providers and reads every request on the way out.
              Secrets are stopped, personal data is redacted, and you get a record of what nearly left — without
              the record itself holding any of it.
            </p>
            <div style={{ display: 'flex', gap: 10, flexWrap: 'wrap', alignItems: 'center' }}>
              {registration ? (
                <Link to="/signup" className="ap-accent-btn" style={{ background: 'var(--accent)', color: 'var(--on-accent)', padding: '11px 22px', borderRadius: 8, fontSize: 14.5, fontWeight: 600 }}>
                  Create an account
                </Link>
              ) : (
                <Link to="/login" className="ap-accent-btn" style={{ background: 'var(--accent)', color: 'var(--on-accent)', padding: '11px 22px', borderRadius: 8, fontSize: 14.5, fontWeight: 600 }}>
                  Sign in to your organization
                </Link>
              )}
              <a href="https://github.com/danilovid/aperture#quickstart-first-caught-secret-in-2-minutes" className="ap-save-btn" style={{ background: 'var(--bg3)', color: 'var(--text)', border: '1px solid var(--border2)', padding: '10px 20px', borderRadius: 8, fontSize: 14.5, fontWeight: 600 }}>
                Run it yourself
              </a>
            </div>
          </div>

          {/* The one picture: an agent's request, stopped on its way out. */}
          <div style={{ ...card, padding: 0, overflow: 'hidden', boxShadow: 'var(--shadow)' }} aria-label="An example of a blocked request">
            <div style={{ padding: '14px 18px', borderBottom: '1px solid var(--border)', display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={{ width: 8, height: 8, borderRadius: '50%', background: 'var(--red)', display: 'inline-block' }} />
              <span style={{ fontSize: 13, fontWeight: 600 }}>ci-bot → gpt-4o-mini</span>
              <span style={{ flex: 1 }} />
              <ProviderBadge provider="openai" />
            </div>
            <pre style={{ ...mono, margin: 0, padding: '16px 18px', fontSize: 12.5, lineHeight: 1.7, color: 'var(--muted)', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
{`POST /v1/chat/completions
{
  "messages": [{
    "role": "user",
    "content": "deploy with `}<span aria-label="a secret, blacked out" style={{ background: 'var(--text)', color: 'transparent', borderRadius: 2, userSelect: 'none' }}>AKIAIOSFODNN7EXAMPL</span>{`"
  }]
}`}
            </pre>
            <div style={{ padding: '13px 18px', borderTop: '1px solid var(--border)', background: 'var(--bg3)', display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <ActionBadge action="blocked" />
              <span style={{ ...mono, fontSize: 12.5 }}>aws-access-key</span>
              <span style={{ flex: 1 }} />
              <span style={{ ...mono, fontSize: 12, color: 'var(--faint)' }}>403 · never reached the provider</span>
            </div>
          </div>
        </section>

        <section className="ap-landing-flow" aria-label="How it fits">
          {['agents & apps', 'Aperture', 'OpenAI · Anthropic · Groq'].map((step, i) => (
            <div key={step} style={{ display: 'contents' }}>
              {i > 0 && <span style={{ color: 'var(--faint)', fontSize: 18 }} aria-hidden="true">→</span>}
              <div style={{ ...card, padding: '12px 18px', textAlign: 'center', ...(i === 1 ? { borderColor: 'var(--accent)', background: 'var(--accent-dim)', color: 'var(--accent)', fontWeight: 700 } : { color: 'var(--muted)' }) }}>
                {i === 1 ? 'scan · block · redact · log' : step}
                {i === 1 && <div style={{ fontSize: 11.5, fontWeight: 500, color: 'var(--muted)', marginTop: 2 }}>Aperture</div>}
              </div>
            </div>
          ))}
        </section>

        <section className="ap-landing-grid">
          {features.map((f) => (
            <div key={f.title} style={{ ...card, padding: '20px 22px' }}>
              <div style={{ fontWeight: 600, fontSize: 15, marginBottom: 8 }}>{f.title}</div>
              <div style={{ color: 'var(--muted)', fontSize: 13.5 }}>{f.body}</div>
            </div>
          ))}
        </section>

        <section style={{ ...card, padding: '22px 24px', display: 'grid', gap: 12 }}>
          <div style={{ fontWeight: 600, fontSize: 15 }}>One line for the agent</div>
          <pre style={{ ...mono, margin: 0, background: 'var(--bg)', border: '1px solid var(--border)', borderRadius: 8, padding: '14px 16px', fontSize: 12.5, overflowX: 'auto' }}>
{`export OPENAI_BASE_URL=https://aperture.your-company.internal/v1
export OPENAI_API_KEY=ap-…   # an Aperture key, not the provider's`}
          </pre>
          <div style={{ color: 'var(--muted)', fontSize: 13 }}>
            The provider's own key stays on the gateway. Agents never hold it, so an agent that leaks its
            configuration leaks nothing that works anywhere else.
          </div>
        </section>
      </main>

      <footer className="ap-landing-foot">
        <span>
          {registration ? (
            <>
              Already have an account? <Link to="/login">Sign in</Link>
            </>
          ) : (
            'Accounts are by invitation — ask an admin of your organization for a link.'
          )}
        </span>
        <span style={{ ...mono, fontSize: 12 }}>Apache-2.0 · self-hosted</span>
      </footer>
    </div>
  )
}
