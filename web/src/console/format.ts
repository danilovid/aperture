// Human-friendly number/time formatting for the console.

export function fmtNum(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k'
  return String(n)
}

export function fmtCost(usd: number): string {
  if (usd === 0) return '$0'
  if (usd < 0.01) return '$' + usd.toFixed(4)
  if (usd < 1) return '$' + usd.toFixed(3)
  return '$' + usd.toFixed(2)
}

export function fmtMs(ms: number): string {
  if (ms >= 10_000) return (ms / 1000).toFixed(1) + ' s'
  return Math.round(ms) + ' ms'
}

export function fmtPct(frac: number): string {
  return (frac * 100).toFixed(2).replace(/\.?0+$/, '') + '%'
}

export function timeAgo(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return iso
  const s = Math.max(0, (Date.now() - t) / 1000)
  if (s < 60) return 'now'
  if (s < 3600) return Math.floor(s / 60) + 'm ago'
  if (s < 86400) return Math.floor(s / 3600) + 'h ago'
  return Math.floor(s / 86400) + 'd ago'
}

export function fmtTs(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleString()
}

export function maskKey(key: string): string {
  if (key.length <= 10) return '*'.repeat(key.length)
  return key.slice(0, 7) + '************' + key.slice(-4)
}
