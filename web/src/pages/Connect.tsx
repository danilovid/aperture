// The guides, for anybody: how to point each tool at this gateway, and what
// the gateway then does to its traffic. The selected tool lives in the URL,
// /connect/<tool>, so a guide can be linked to.
import { navigate } from '../router'
import { card } from '../console/styles'
import { ConnectGuide } from '../connect/ConnectGuide'
import { gatewayBase } from '../connect/base'
import { explainer, guides } from '../connect/guides'
import { SiteHeader } from './SiteHeader'
import type { Theme } from '../theme'

export function Connect({
  theme,
  toggleTheme,
  tool,
  signedIn,
}: {
  theme: Theme
  toggleTheme: () => void
  tool?: string
  signedIn: boolean
}) {
  const known = guides.some((g) => g.id === tool) ? tool : undefined
  return (
    <div className="ap-root" data-ap-theme={theme}>
      <SiteHeader theme={theme} toggleTheme={toggleTheme} signedIn={signedIn} />
      <main className="ap-landing" style={{ maxWidth: 960 }}>
        <section>
          <h1 className="ap-display" style={{ fontSize: 'clamp(28px, 3.6vw, 40px)', margin: 0, fontWeight: 700, letterSpacing: '-1px' }}>
            Connect your agent
          </h1>
          <p style={{ fontSize: 15, color: 'var(--muted)', maxWidth: 640, margin: '12px 0 0' }}>
            Point the tool at this gateway instead of the provider, with a Mutegate key instead of the provider's.
            The provider's own key stays on the gateway. Keys are created in the console, under Settings → API keys,
            where these same snippets come with the key filled in.
          </p>
        </section>

        <section style={{ ...card, padding: '22px 24px' }}>
          <ConnectGuide base={gatewayBase()} tool={known} onTool={(id) => navigate(`/connect/${id}`, { replace: true })} />
        </section>

        <section>
          <h2 className="ap-display" style={{ fontSize: 20, margin: '0 0 12px' }}>What happens to the traffic</h2>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(min(100%, 380px), 1fr))', gap: 14 }}>
            {explainer.map((e) => (
              <div key={e.title} style={{ ...card, padding: '18px 20px' }}>
                <div style={{ fontWeight: 600, fontSize: 14.5, marginBottom: 6 }}>{e.title}</div>
                <div style={{ color: 'var(--muted)', fontSize: 13.5, lineHeight: 1.55 }}>{e.body}</div>
              </div>
            ))}
          </div>
        </section>
      </main>
    </div>
  )
}
