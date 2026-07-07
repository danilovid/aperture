
class Component extends DCLogic {
  state = {
    screen: null,
    theme: 'dark',
    period: '7d',
    loading: false,
    metric: 'requests',
    fAction: 'all', fRule: 'all', fKey: 'all',
    selId: null,
    policyKey: 'ci-agent',
    policies: {},
    customRules: [
      { name: 'project-x', pattern: 'project-x' },
      { name: 'internal-domain', pattern: '\\.internal\\b' },
    ],
    newRuleName: '', newRulePattern: '',
    previewText: 'Deploy notes: use AKIAIOSFODNN7EXAMPLE for staging,\nping ivan.petrov@corp.io about project-x rollout.',
    providerVals: { openai: 'sk-....configured', anthropic: 'sk-ant-....configured', groq: '' },
    providerConfigured: { openai: true, anthropic: true, groq: false },
    apKeys: [
      { id: 1, name: 'ci-agent', masked: 'ap_k1_9f3e************c2a1', created: '2026-05-12', policy: 'strict' },
      { id: 2, name: 'dev-ivan', masked: 'ap_k1_77b0************e83d', created: '2026-05-20', policy: 'default' },
      { id: 3, name: 'backend-prod', masked: 'ap_k1_c41a************09fe', created: '2026-06-02', policy: 'strict' },
      { id: 4, name: 'qa-bot', masked: 'ap_k1_2d8c************b7a4', created: '2026-06-18', policy: 'audit-only' },
      { id: 5, name: 'ml-batch', masked: 'ap_k1_e59b************1d20', created: '2026-06-30', policy: 'default' },
    ],
    newKeyName: '', newKeyPolicy: 'default',
    confirmDelete: null,
    toasts: [],
    revealedKey: null,
  };

  componentDidMount() {
    try {
      if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
        this.setState({ theme: 'light' });
      }
    } catch (e) {}
    // default policies per key
    const pol = {};
    ['ci-agent','dev-ivan','backend-prod','qa-bot','ml-batch'].forEach((k) => {
      pol[k] = {
        secrets: { on: true, action: 'block' },
        pii: { on: true, action: 'redact' },
        custom: { on: k !== 'qa-bot', action: 'alert' },
      };
    });
    this.setState({ policies: pol });
  }

  toast(msg) {
    const id = Date.now() + Math.random();
    this.setState((s) => ({ toasts: [...s.toasts, { id, msg }] }));
    setTimeout(() => {
      this.setState((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) }));
    }, 2600);
  }

  // deterministic pseudo-random
  r(seed) { const x = Math.sin(seed * 127.1 + 311.7) * 43758.5453; return x - Math.floor(x); }

  fmtK(n) {
    if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
    if (n >= 1e4) return (n / 1e3).toFixed(0) + 'k';
    if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
    return String(Math.round(n));
  }
  fmtCost(c) {
    if (c >= 100) return '$' + c.toFixed(0);
    if (c >= 1) return '$' + c.toFixed(2);
    if (c >= 0.01) return '$' + c.toFixed(3);
    return '$' + c.toFixed(4);
  }

  spark(arr, color) {
    const min = Math.min(...arr), max = Math.max(...arr), range = max - min || 1;
    const n = arr.length;
    return arr.map((v, i) => {
      const x = (i / (n - 1)) * 72;
      const y = 22 - ((v - min) / range) * 20;
      return x.toFixed(1) + ',' + y.toFixed(1);
    }).join(' ');
  }

  getEvents() {
    return [
      { id: 1, time: '2m ago', ts: '2026-07-07 14:52:11 UTC', key: 'ci-agent', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'aws-key', action: 'blocked', sample: 'AKIA****************', desc: 'AWS access key ID detected in prompt body', reqId: 'req_8f2ea1c4' },
      { id: 2, time: '18m ago', ts: '2026-07-07 14:36:03 UTC', key: 'dev-ivan', model: 'claude-3-5-sonnet', provider: 'Anthropic', rule: 'github-token', action: 'blocked', sample: 'ghp_************************', desc: 'GitHub personal access token detected', reqId: 'req_b31d09aa' },
      { id: 3, time: '41m ago', ts: '2026-07-07 14:13:47 UTC', key: 'backend-prod', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'email', action: 'redacted', sample: 'i***.petrov@*******.io', desc: 'Email address redacted before forwarding', reqId: 'req_44c7e2f0' },
      { id: 4, time: '1h ago', ts: '2026-07-07 13:49:20 UTC', key: 'qa-bot', model: 'llama-3.3-70b', provider: 'Groq', rule: 'credit-card', action: 'redacted', sample: '4242 42** **** ****', desc: 'Payment card number redacted (Luhn-valid)', reqId: 'req_09aa31be' },
      { id: 5, time: '2h ago', ts: '2026-07-07 12:58:02 UTC', key: 'dev-ivan', model: 'claude-3-5-sonnet', provider: 'Anthropic', rule: 'custom:project-x', action: 'alerted', sample: '…codename "project-x" launch…', desc: 'Custom stop-word matched; request forwarded, alert raised', reqId: 'req_e1c48d77' },
      { id: 6, time: '3h ago', ts: '2026-07-07 11:41:55 UTC', key: 'ci-agent', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'private-key', action: 'blocked', sample: '-----BEGIN RSA PRIVATE K***', desc: 'PEM private key block detected', reqId: 'req_72fd10c3' },
      { id: 7, time: '5h ago', ts: '2026-07-07 09:30:18 UTC', key: 'backend-prod', model: 'claude-3-5-sonnet', provider: 'Anthropic', rule: 'phone', action: 'redacted', sample: '+7 9** ***-**-**', desc: 'Phone number redacted before forwarding', reqId: 'req_5a80b9e2' },
      { id: 8, time: '7h ago', ts: '2026-07-07 07:22:40 UTC', key: 'ml-batch', model: 'llama-3.3-70b', provider: 'Groq', rule: 'aws-key', action: 'blocked', sample: 'AKIA****************', desc: 'AWS access key ID detected in prompt body', reqId: 'req_c93f61d8' },
      { id: 9, time: '9h ago', ts: '2026-07-07 05:14:09 UTC', key: 'dev-ivan', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'email', action: 'redacted', sample: 'a***@gmail.com', desc: 'Email address redacted before forwarding', reqId: 'req_16de84fb' },
      { id: 10, time: '12h ago', ts: '2026-07-07 02:44:31 UTC', key: 'qa-bot', model: 'claude-3-5-sonnet', provider: 'Anthropic', rule: 'custom:internal-domain', action: 'alerted', sample: 'https://git.****.internal/…', desc: 'Internal domain referenced; alert raised', reqId: 'req_a05c72e9' },
      { id: 11, time: '1d ago', ts: '2026-07-06 18:05:56 UTC', key: 'ci-agent', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'slack-token', action: 'blocked', sample: 'xoxb-********-********', desc: 'Slack bot token detected', reqId: 'req_dd41f0a7' },
      { id: 12, time: '1d ago', ts: '2026-07-06 12:37:12 UTC', key: 'backend-prod', model: 'llama-3.3-70b', provider: 'Groq', rule: 'credit-card', action: 'redacted', sample: '5500 00** **** ****', desc: 'Payment card number redacted (Luhn-valid)', reqId: 'req_38b2c5e0' },
      { id: 13, time: '2d ago', ts: '2026-07-05 16:20:48 UTC', key: 'ml-batch', model: 'claude-3-5-sonnet', provider: 'Anthropic', rule: 'iban', action: 'redacted', sample: 'DE89 3704 **** **** **', desc: 'IBAN account number redacted', reqId: 'req_91e7a3cd' },
      { id: 14, time: '2d ago', ts: '2026-07-05 10:02:33 UTC', key: 'dev-ivan', model: 'gpt-4o-mini', provider: 'OpenAI', rule: 'aws-secret', action: 'blocked', sample: 'wJalrXUtnFEMI/K7********', desc: 'AWS secret access key detected', reqId: 'req_6c30fb12' },
    ];
  }

  actionStyle(action) {
    if (action === 'blocked') return { bg: 'var(--red-bg)', fg: 'var(--red)', label: 'BLOCKED' };
    if (action === 'redacted') return { bg: 'var(--amber-bg)', fg: 'var(--amber)', label: 'REDACTED' };
    return { bg: 'var(--accent-dim)', fg: 'var(--accent)', label: 'ALERT' };
  }
  provStyle(p) {
    if (p === 'OpenAI') return { bg: 'var(--green-bg)', fg: 'var(--green)' };
    if (p === 'Anthropic') return { bg: 'var(--amber-bg)', fg: 'var(--amber)' };
    return { bg: 'var(--accent-dim)', fg: 'var(--accent)' };
  }

  setPeriod(p) {
    if (p === this.state.period) return;
    this.setState({ period: p, loading: true });
    clearTimeout(this._lt);
    this._lt = setTimeout(() => this.setState({ loading: false }), 450);
  }

  runPreview() {
    const key = this.state.policyKey;
    const pol = this.state.policies[key] || { secrets: { on: true, action: 'block' }, pii: { on: true, action: 'redact' }, custom: { on: true, action: 'alert' } };
    const text = this.state.previewText || '';
    const hits = [];
    let out = text;
    const push = (group, rule, m, mask) => {
      const action = pol[group].action;
      hits.push({ rule, action, match: m });
      if (action !== 'alert') out = out.split(m).join(mask);
    };
    if (pol.secrets && pol.secrets.on) {
      const aws = text.match(/AKIA[A-Z0-9]{16}/g) || [];
      aws.forEach((m) => push('secrets', 'aws-key', m, 'AKIA****************'));
      const gh = text.match(/ghp_[A-Za-z0-9]{10,}/g) || [];
      gh.forEach((m) => push('secrets', 'github-token', m, 'ghp_************'));
      const pem = text.match(/-----BEGIN [A-Z ]*PRIVATE KEY-----/g) || [];
      pem.forEach((m) => push('secrets', 'private-key', m, '-----BEGIN ***-----'));
    }
    if (pol.pii && pol.pii.on) {
      const em = text.match(/[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-z]{2,}/g) || [];
      em.forEach((m) => push('pii', 'email', m, m[0] + '***@***'));
      const cc = text.match(/\b\d{4}[ -]?\d{4}[ -]?\d{4}[ -]?\d{4}\b/g) || [];
      cc.forEach((m) => push('pii', 'credit-card', m, m.slice(0, 4) + ' **** **** ****'));
    }
    if (pol.custom && pol.custom.on) {
      this.state.customRules.forEach((r) => {
        let re = null;
        try { re = new RegExp(r.pattern, 'gi'); } catch (e) { re = null; }
        const ms = re ? (text.match(re) || []) : (text.toLowerCase().includes(r.pattern.toLowerCase()) ? [r.pattern] : []);
        ms.slice(0, 3).forEach((m) => push('custom', 'custom:' + r.name, m, '[' + r.name + ']'));
      });
    }
    const blocked = hits.some((h) => h.action === 'block');
    return { hits, out: blocked ? '— request blocked, nothing sent —' : out };
  }

  renderVals() {
    const s = this.state;
    const screen = s.screen || this.props.defaultScreen || 'events';
    const setScreen = (sc) => this.setState({ screen: sc, selId: null });
    const period = s.period;

    // ---- period-scaled stats ----
    const factor = period === '24h' ? 1 : period === '7d' ? 6.4 : 26.5;
    const nBuckets = period === '24h' ? 24 : period === '7d' ? 14 : 30;
    const seedBase = period === '24h' ? 3 : period === '7d' ? 41 : 97;

    const buckets = [];
    for (let i = 0; i < nBuckets; i++) {
      const req = Math.round(120 + this.r(seedBase + i) * 320 + (i % 7 < 5 ? 90 : -40));
      const tok = Math.round(req * (600 + this.r(seedBase + i + 50) * 700));
      buckets.push({
        requests: Math.max(30, req),
        tokens: tok,
        cost: tok * 0.0000021 * (1 + this.r(seedBase + i + 99)),
        latency: Math.round(320 + this.r(seedBase + i + 150) * 480),
        dlp: Math.round(this.r(seedBase + i + 200) * 3.4),
      });
    }
    const sum = (k) => buckets.reduce((a, b) => a + b[k], 0);
    const requests = sum('requests');
    const tokens = sum('tokens');
    const cost = sum('cost');
    const latency = Math.round(sum('latency') / nBuckets);
    const dlpTotal = sum('dlp');
    const dlpBlocked = Math.round(dlpTotal * 0.42);
    const dlpRedacted = Math.round(dlpTotal * 0.47);
    const errRate = (0.4 + this.r(seedBase + 7) * 0.8);

    const sparkOf = (k) => this.spark(buckets.map((b) => b[k]));
    const kpis = [
      { label: 'Requests', value: this.fmtK(requests), delta: '+12.4%', deltaColor: 'var(--green)', sub: 'vs prev period', spark: sparkOf('requests'), sparkColor: 'var(--accent)' },
      { label: 'DLP events', value: String(dlpTotal), delta: '−8.1%', deltaColor: 'var(--green)', sub: dlpBlocked + ' blocked · ' + dlpRedacted + ' redacted', spark: sparkOf('dlp'), sparkColor: 'var(--red)' },
      { label: 'Total tokens', value: this.fmtK(tokens), delta: '+9.7%', deltaColor: 'var(--green)', sub: 'vs prev period', spark: sparkOf('tokens'), sparkColor: 'var(--accent)' },
      { label: 'Cost', value: this.fmtCost(cost), delta: '+15.2%', deltaColor: 'var(--amber)', sub: 'vs prev period', spark: sparkOf('cost'), sparkColor: 'var(--amber)' },
      { label: 'Avg latency', value: latency + ' ms', delta: '−4.3%', deltaColor: 'var(--green)', sub: 'vs prev period', spark: sparkOf('latency'), sparkColor: 'var(--accent)' },
      { label: 'Error rate', value: errRate.toFixed(2) + '%', delta: '+0.1pp', deltaColor: 'var(--muted)', sub: 'mostly 429s', spark: sparkOf('dlp'), sparkColor: 'var(--muted)' },
    ];

    // ---- chart ----
    const metricMap = { requests: 'requests', dlp: 'dlp', cost: 'cost', latency: 'latency' };
    const mk = metricMap[s.metric] || 'requests';
    const vals = buckets.map((b) => b[mk]);
    const vmax = Math.max(...vals) || 1;
    const fmtVal = (v) => mk === 'cost' ? this.fmtCost(v) : mk === 'latency' ? Math.round(v) + ' ms' : this.fmtK(v);
    const labels = period === '24h'
      ? buckets.map((b, i) => (i) + ':00')
      : buckets.map((b, i) => 'Day ' + (i + 1));
    const bars = buckets.map((b, i) => ({
      h: Math.max(2, Math.round((vals[i] / vmax) * 100)) + '%',
      tip: labels[i] + ' · ' + fmtVal(vals[i]),
    }));
    const chartStart = period === '24h' ? '00:00' : period === '7d' ? 'Jun 30' : 'Jun 8';
    const chartEnd = 'now';

    const metricTabs = [
      { id: 'requests', label: 'Requests' },
      { id: 'dlp', label: 'DLP events' },
      { id: 'cost', label: 'Cost' },
      { id: 'latency', label: 'Latency' },
    ].map((m) => ({
      ...m,
      set: () => this.setState({ metric: m.id }),
      bg: s.metric === m.id ? 'var(--bg2)' : 'transparent',
      fg: s.metric === m.id ? 'var(--text)' : 'var(--muted)',
    }));

    const periodTabs = ['24h', '7d', '30d'].map((p) => ({
      label: p,
      set: () => this.setPeriod(p),
      bg: period === p ? 'var(--accent-dim)' : 'transparent',
      fg: period === p ? 'var(--accent)' : 'var(--muted)',
    }));

    const modelData = [
      { model: 'gpt-4o-mini', provider: 'OpenAI', req: 2140, tokPer: 720, costPer: 0.00043, lat: 410 },
      { model: 'claude-3-5-sonnet', provider: 'Anthropic', req: 1480, tokPer: 1150, costPer: 0.0041, lat: 780 },
      { model: 'llama-3.3-70b', provider: 'Groq', req: 1203, tokPer: 540, costPer: 0.00019, lat: 210 },
    ];
    const modelRows = modelData.map((m) => {
      const ps = this.provStyle(m.provider);
      const req = Math.round(m.req * factor / 6.4 * (period === '24h' ? 1 : period === '7d' ? 6.4 : 26.5) / (period === '24h' ? 6.4 : period === '7d' ? 6.4 : 6.4));
      const reqs = Math.round(m.req * (factor / 6.4));
      return {
        model: m.model, provider: m.provider, provBg: ps.bg, provFg: ps.fg,
        requests: this.fmtK(reqs),
        tokens: this.fmtK(reqs * m.tokPer),
        cost: this.fmtCost(reqs * m.costPer),
        latency: m.lat + ' ms',
      };
    });

    // ---- events ----
    const all = this.getEvents();
    const filtered = all.filter((e) =>
      (s.fAction === 'all' || e.action === s.fAction) &&
      (s.fRule === 'all' || e.rule === s.fRule) &&
      (s.fKey === 'all' || e.key === s.fKey)
    );
    const demoEmpty = this.props.demoCleanTraffic ?? false;
    const shown = demoEmpty ? [] : filtered;
    const events = shown.map((e) => {
      const a = this.actionStyle(e.action);
      return {
        ...e, actionLabel: a.label, badgeBg: a.bg, badgeFg: a.fg,
        rowBg: s.selId === e.id ? 'var(--bg3)' : 'transparent',
        open: () => this.setState({ selId: s.selId === e.id ? null : e.id }),
      };
    });
    const sel = all.find((e) => e.id === s.selId) || null;
    const selA = sel ? this.actionStyle(sel.action) : { bg: '', fg: '', label: '' };

    const ruleOptions = [...new Set(all.map((e) => e.rule))].map((v) => ({ v }));
    const keyOptions = [...new Set(all.map((e) => e.key))].map((v) => ({ v }));

    // ---- policies ----
    const pol = s.policies[s.policyKey] || { secrets: { on: true, action: 'block' }, pii: { on: true, action: 'redact' }, custom: { on: true, action: 'alert' } };
    const setPol = (group, patch) => this.setState((st) => ({
      policies: { ...st.policies, [st.policyKey]: { ...st.policies[st.policyKey], [group]: { ...st.policies[st.policyKey][group], ...patch } } }
    }));
    const groupDefs = [
      { id: 'secrets', name: 'Secrets', desc: 'AWS keys, GitHub / Slack tokens, PEM private keys, generic high-entropy strings' },
      { id: 'pii', name: 'PII', desc: 'Emails, payment cards, phone numbers, IBAN account numbers' },
      { id: 'custom', name: 'Custom rules', desc: 'Your regexes and stop-words (configured below)' },
    ];
    const actionDefs = [
      { id: 'block', label: 'Block', fg: 'var(--red)', bgOn: 'var(--red-bg)' },
      { id: 'redact', label: 'Redact', fg: 'var(--amber)', bgOn: 'var(--amber-bg)' },
      { id: 'alert', label: 'Alert only', fg: 'var(--accent)', bgOn: 'var(--accent-dim)' },
    ];
    const policyGroups = groupDefs.map((g) => {
      const gp = pol[g.id];
      return {
        name: g.name, desc: g.desc, on: gp.on,
        switchBg: gp.on ? 'var(--accent)' : 'var(--bg4)',
        knobLeft: gp.on ? '19px' : '3px',
        toggle: () => setPol(g.id, { on: !gp.on }),
        actions: actionDefs.map((a) => ({
          label: a.label,
          set: () => setPol(g.id, { action: a.id }),
          bg: gp.action === a.id ? a.bgOn : 'transparent',
          fg: gp.action === a.id ? a.fg : 'var(--muted)',
        })),
      };
    });
    const policyKeyTabs = s.apKeys.map((k) => ({
      label: k.name,
      set: () => this.setState({ policyKey: k.name }),
      bg: s.policyKey === k.name ? 'var(--accent-dim)' : 'var(--bg2)',
      fg: s.policyKey === k.name ? 'var(--accent)' : 'var(--muted)',
      bd: s.policyKey === k.name ? 'var(--accent)' : 'var(--border)',
    }));
    const customRules = s.customRules.map((r, i) => ({
      ...r,
      remove: () => { this.setState((st) => ({ customRules: st.customRules.filter((x, j) => j !== i) })); this.toast('Rule custom:' + r.name + ' removed'); },
    }));
    const addRule = () => {
      const n = s.newRuleName.trim(), p = s.newRulePattern.trim();
      if (!n || !p) { this.toast('Name and pattern are required'); return; }
      this.setState((st) => ({ customRules: [...st.customRules, { name: n, pattern: p }], newRuleName: '', newRulePattern: '' }));
      this.toast('Rule custom:' + n + ' added');
    };

    const preview = this.runPreview();
    const previewHits = preview.hits.map((h) => {
      const st = h.action === 'block' ? this.actionStyle('blocked') : h.action === 'redact' ? this.actionStyle('redacted') : this.actionStyle('alerted');
      return { rule: h.rule, action: st.label, bg: st.bg, fg: st.fg, match: h.match };
    });

    // ---- settings ----
    const provDefs = [
      { id: 'openai', name: 'OpenAI', placeholder: 'sk-...' },
      { id: 'anthropic', name: 'Anthropic', placeholder: 'sk-ant-...' },
      { id: 'groq', name: 'Groq', placeholder: 'gsk_...' },
    ];
    const providers = provDefs.map((p) => {
      const ps = this.provStyle(p.name);
      const conf = s.providerConfigured[p.id];
      return {
        name: p.name, placeholder: p.placeholder, badgeBg: ps.bg, badgeFg: ps.fg,
        value: s.providerVals[p.id],
        onChange: (e) => this.setState((st) => ({ providerVals: { ...st.providerVals, [p.id]: e.target.value } })),
        status: conf ? 'configured' : 'not set',
        statusColor: conf ? 'var(--green)' : 'var(--faint)',
        save: () => {
          if (!s.providerVals[p.id]) { this.toast('Enter a key first'); return; }
          this.setState((st) => ({ providerConfigured: { ...st.providerConfigured, [p.id]: true } }));
          this.toast(p.name + ' key saved');
        },
        clear: () => {
          this.setState((st) => ({ providerVals: { ...st.providerVals, [p.id]: '' }, providerConfigured: { ...st.providerConfigured, [p.id]: false } }));
          this.toast(p.name + ' key cleared');
        },
      };
    });

    const apKeys = s.apKeys.map((k) => ({
      name: k.name,
      masked: s.revealedKey === k.id ? k.masked.replace('************', 'Qw7RtY2uIoP5') : k.masked,
      keyColor: s.revealedKey === k.id ? 'var(--green)' : 'var(--muted)',
      created: k.created, policy: k.policy,
      confirming: s.confirmDelete === k.id,
      notConfirming: s.confirmDelete !== k.id,
      del: () => this.setState({ confirmDelete: k.id }),
      cancelDel: () => this.setState({ confirmDelete: null }),
      confirmDel: () => {
        this.setState((st) => ({ apKeys: st.apKeys.filter((x) => x.id !== k.id), confirmDelete: null }));
        this.toast('Key ' + k.name + ' deleted');
      },
    }));
    const createKey = () => {
      const n = s.newKeyName.trim();
      if (!n) { this.toast('Give the key a name'); return; }
      if (s.apKeys.some((k) => k.name === n)) { this.toast('A key with that name exists'); return; }
      const id = Math.max(...s.apKeys.map((k) => k.id), 0) + 1;
      const suffix = Math.random().toString(16).slice(2, 6);
      this.setState((st) => ({
        apKeys: [...st.apKeys, { id, name: n, masked: 'ap_k1_' + Math.random().toString(16).slice(2, 6) + '************' + suffix, created: '2026-07-07', policy: st.newKeyPolicy }],
        newKeyName: '', revealedKey: id,
      }));
      this.toast('Key ' + n + ' created — copy it now, it is shown once');
    };

    const navItems = [
      { id: 'dashboard', label: 'Overview' },
      { id: 'events', label: 'DLP Events', badge: String(all.length) },
      { id: 'policies', label: 'Policies' },
      { id: 'settings', label: 'Settings & Keys' },
    ].map((n) => ({
      ...n,
      go: () => setScreen(n.id),
      bg: screen === n.id ? 'var(--accent-dim)' : 'transparent',
      fg: screen === n.id ? 'var(--accent)' : 'var(--muted)',
      badge: n.badge || false,
    }));

    return {
      theme: s.theme,
      accent: this.props.accent ?? undefined,
      themeLabel: s.theme === 'dark' ? '☀ Light theme' : '● Dark theme',
      toggleTheme: () => this.setState({ theme: s.theme === 'dark' ? 'light' : 'dark' }),
      isLanding: screen === 'landing',
      isAdmin: screen !== 'landing',
      isDashboard: screen === 'dashboard',
      isEvents: screen === 'events',
      isPolicies: screen === 'policies',
      isSettings: screen === 'settings',
      goDashboard: () => setScreen('dashboard'),
      goLanding: () => setScreen('landing'),
      navItems,
      // dashboard
      periodTabs, kpis, loading: s.loading, notLoading: !s.loading,
      metricTabs, bars, chartStart, chartEnd, modelRows,
      // events
      fAction: s.fAction, fRule: s.fRule, fKey: s.fKey,
      setFAction: (e) => this.setState({ fAction: e.target.value, selId: null }),
      setFRule: (e) => this.setState({ fRule: e.target.value, selId: null }),
      setFKey: (e) => this.setState({ fKey: e.target.value, selId: null }),
      ruleOptions, keyOptions,
      eventCount: shown.length + ' of ' + all.length + ' events',
      events, eventsEmpty: shown.length === 0, eventsNotEmpty: shown.length > 0,
      hasSel: !!sel,
      selAction: selA.label, selBadgeBg: selA.bg, selBadgeFg: selA.fg,
      selRule: sel ? sel.rule : '', selDesc: sel ? sel.desc : '',
      selTs: sel ? sel.ts : '', selKey: sel ? sel.key : '', selModel: sel ? sel.model : '',
      selProvider: sel ? sel.provider : '', selReqId: sel ? sel.reqId : '', selSample: sel ? sel.sample : '',
      closeSel: () => this.setState({ selId: null }),
      // policies
      policyKeyTabs, policyGroups, customRules,
      newRuleName: s.newRuleName, newRulePattern: s.newRulePattern,
      setNewRuleName: (e) => this.setState({ newRuleName: e.target.value }),
      setNewRulePattern: (e) => this.setState({ newRulePattern: e.target.value }),
      addRule,
      previewText: s.previewText,
      setPreviewText: (e) => this.setState({ previewText: e.target.value }),
      previewHits, previewClean: preview.hits.length === 0, previewDirty: preview.hits.length > 0,
      previewResult: preview.out,
      // settings
      providers, apKeys,
      newKeyName: s.newKeyName, newKeyPolicy: s.newKeyPolicy,
      setNewKeyName: (e) => this.setState({ newKeyName: e.target.value }),
      setNewKeyPolicy: (e) => this.setState({ newKeyPolicy: e.target.value }),
      createKey,
      // toasts
      toasts: s.toasts,
    };
  }
}
