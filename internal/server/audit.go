package server

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/mutegate/mutegate/internal/alerter"
	"github.com/mutegate/mutegate/internal/auth"
	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/limits"
	"github.com/mutegate/mutegate/internal/storage"
)

// The audit log answers "who did that": who created a key, weakened a policy,
// unmuted a rule, invited somebody, changed a role, pointed a provider through
// a different proxy. Agent traffic has its own records; this is the journal of
// the people — and the tokens, and the operator — who change what the traffic
// is allowed to do.
//
// An entry is written after the change has landed, and only then: a refused
// or failed request changed nothing, so there is nothing to account for. And
// a failure to write the entry does not fail the request. The change is
// already made, and answering "error" about a change that happened would send
// somebody off to make it twice.

// defaultTarget is what an entry about the organization's default policy or
// limits names as its target. Entries about one key carry its id in meta,
// which is what tells the two apart — a key may well be called "default".
const defaultTarget = "default"

// actor is who is behind a request, as the journal names them.
type actor struct {
	Kind  string
	ID    string
	Label string
}

// userActor names a person.
func userActor(u *storage.User) actor {
	return actor{Kind: storage.ActorUser, ID: u.ID, Label: u.Email}
}

// actorOf works out who made an administrative request. It runs after the
// handler has let the request through, so the credential is known to be good;
// this only has to put a name to it.
func (h *Handlers) actorOf(r *http.Request) actor {
	if c := callerOf(r); c != nil {
		return userActor(c.User)
	}
	if presented := extractBearerToken(r); strings.HasPrefix(presented, serviceTokenPrefix) && h.AccountStore != nil {
		if t, err := h.AccountStore.ServiceTokenByHash(r.Context(), auth.HashSessionToken(presented)); err == nil {
			return actor{Kind: storage.ActorToken, ID: t.ID, Label: t.Name}
		}
	}
	return actor{Kind: storage.ActorOperator, Label: "operator"}
}

// audit records that the caller did something to an organization.
func (h *Handlers) audit(r *http.Request, orgID, action, target string, meta map[string]any) {
	h.auditAs(r, h.actorOf(r), orgID, action, target, meta)
}

// auditChange records a save, unless it saved what was already there. A
// console's save button is pressed with nothing edited often enough that
// journaling each press would bury the saves that did something.
func (h *Handlers) auditChange(r *http.Request, orgID, action, target string, meta map[string]any) {
	if meta["changes"] == nil {
		return
	}
	h.audit(r, orgID, action, target, meta)
}

// auditAs records it for somebody the request does not carry yet — a person
// who has just registered and whose session is being issued in this response.
func (h *Handlers) auditAs(r *http.Request, who actor, orgID, action, target string, meta map[string]any) {
	if h.AuditStore == nil || orgID == "" {
		return
	}
	// Detached from the request: a client that hangs up the moment it has
	// its answer must not take the record of what it did with it.
	ctx := context.WithoutCancel(r.Context())
	if len(meta) == 0 {
		meta = nil
	}
	err := h.AuditStore.Record(ctx, storage.AuditEntry{
		OrgID: orgID, ActorKind: who.Kind, ActorID: who.ID, ActorLabel: who.Label,
		Action: action, Target: target, Meta: meta, IP: clientIP(r),
	})
	if err != nil {
		h.Logger.Error("audit log write failed", "err", err, "org", orgID, "action", action)
	}
}

// GET /admin/audit?group=policy&before=123&limit=50
//
// Owners and admins read it, and the operator naming an organization. Not
// service tokens: a journal of who did what is exactly what a leaked token
// should not be able to read to learn who to impersonate next.
func (h *Handlers) handleAuditList(w http.ResponseWriter, r *http.Request) {
	orgID, ok := h.adminOrg(w, r, storage.RoleAdmin)
	if !ok {
		return
	}
	if h.AuditStore == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error": "the audit log needs a database (this gateway runs without one)",
		})
		return
	}
	q := r.URL.Query()
	f := storage.AuditFilter{Group: strings.TrimSpace(q.Get("group"))}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "before must be an entry id"})
			return
		}
		f.Before = n
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "limit must be a positive number"})
			return
		}
		f.Limit = n
	}
	entries, err := h.AuditStore.List(r.Context(), orgID, f)
	if err != nil {
		h.Logger.Error("audit log read failed", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not load the audit log"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// keyName is what the journal calls a key: its name, which is what people
// know it by, or its id when it is already gone.
func (h *Handlers) keyName(ctx context.Context, orgID, id string) string {
	if h.KeyStore == nil {
		return id
	}
	keys, err := h.KeyStore.List(ctx, orgID)
	if err != nil {
		return id
	}
	for _, k := range keys {
		if k.ID == id {
			return k.Name
		}
	}
	return id
}

// ── What changed ─────────────────────────────────────────────────────────────
//
// A journal that says "policy updated" makes somebody diff two JSON blobs by
// eye at the worst possible moment. These say what moved, and — for a policy —
// whether it moved towards letting more through, which is the question the
// entry will be read to answer.

// strictness orders DLP actions from "sees nothing" to "stops the request".
func strictness(a inspector.Action) int {
	switch a {
	case inspector.ActionBlock:
		return 3
	case inspector.ActionRedact:
		return 2
	case inspector.ActionAlert:
		return 1
	}
	return 0
}

// policyChange describes going from one policy to another.
func policyChange(before, after inspector.Policy) map[string]any {
	var changes []string
	weakened := false
	for _, g := range []struct {
		name string
		b, a inspector.Action
	}{
		{"secrets", before.Secrets, after.Secrets},
		{"pii", before.PII, after.PII},
		{"custom", before.Custom, after.Custom},
	} {
		if g.b == g.a {
			continue
		}
		changes = append(changes, g.name+": "+actionName(g.b)+" → "+actionName(g.a))
		if strictness(g.a) < strictness(g.b) {
			weakened = true
		}
	}

	added, removed := diffSets(ruleNames(before.CustomRules), ruleNames(after.CustomRules))
	for _, n := range added {
		changes = append(changes, "custom rule added: "+n)
	}
	for _, n := range removed {
		changes = append(changes, "custom rule removed: "+n)
		weakened = true
	}
	// A rule kept under its name can still be rewritten to match nothing.
	// Whether a new pattern is narrower is not something to guess at, so a
	// rewrite is named and left for the reader to judge.
	for _, b := range before.CustomRules {
		for _, a := range after.CustomRules {
			if a.Name == b.Name && a.Pattern != b.Pattern {
				changes = append(changes, "custom rule changed: "+a.Name)
			}
		}
	}
	// An allowlist entry is an exemption; a muted rule is a detector turned
	// off. More of either lets more through.
	added, removed = diffSets(before.Allowlist, after.Allowlist)
	for _, a := range added {
		changes = append(changes, "allowlisted: "+a)
		weakened = true
	}
	for _, a := range removed {
		changes = append(changes, "no longer allowlisted: "+a)
	}
	added, removed = diffSets(lower(before.MutedRules), lower(after.MutedRules))
	for _, m := range added {
		changes = append(changes, "muted: "+m)
		weakened = true
	}
	for _, m := range removed {
		changes = append(changes, "unmuted: "+m)
	}
	if before.ScanResponses != after.ScanResponses {
		changes = append(changes, "response scanning "+onOff(after.ScanResponses))
		weakened = weakened || !after.ScanResponses
	}
	if before.NER != after.NER {
		changes = append(changes, "name and address detection "+onOff(after.NER))
		weakened = weakened || !after.NER
	}

	meta := map[string]any{}
	if len(changes) > 0 {
		meta["changes"] = changes
	}
	if weakened {
		meta["weakened"] = true
	}
	return meta
}

func actionName(a inspector.Action) string {
	if a == "" {
		return string(inspector.ActionOff)
	}
	return string(a)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func ruleNames(rules []inspector.CustomRule) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Name)
	}
	return out
}

func lower(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, strings.ToLower(s))
	}
	return out
}

// diffSets returns what is in after and not before, and the other way round,
// each in the order it was listed.
func diffSets(before, after []string) (added, removed []string) {
	for _, a := range after {
		if !slices.Contains(before, a) {
			added = append(added, a)
		}
	}
	for _, b := range before {
		if !slices.Contains(after, b) {
			removed = append(removed, b)
		}
	}
	return added, removed
}

// limitsChange describes a budget or rate limit moving. Zero is "no limit",
// so going to zero is raising it all the way.
func limitsChange(before, after limits.Limits) map[string]any {
	var changes []string
	raised := false
	if before.BudgetDailyUSD != after.BudgetDailyUSD {
		changes = append(changes, "daily budget: "+usd(before.BudgetDailyUSD)+" → "+usd(after.BudgetDailyUSD))
		raised = raised || loosened(before.BudgetDailyUSD, after.BudgetDailyUSD)
	}
	if before.RequestsPerMinute != after.RequestsPerMinute {
		changes = append(changes, "requests per minute: "+rpm(before.RequestsPerMinute)+" → "+rpm(after.RequestsPerMinute))
		raised = raised || loosened(float64(before.RequestsPerMinute), float64(after.RequestsPerMinute))
	}
	meta := map[string]any{}
	if len(changes) > 0 {
		meta["changes"] = changes
	}
	if raised {
		meta["weakened"] = true
	}
	return meta
}

func loosened(before, after float64) bool {
	if after <= 0 {
		return before > 0
	}
	return before > 0 && after > before
}

func usd(v float64) string {
	if v <= 0 {
		return "none"
	}
	return "$" + strconv.FormatFloat(v, 'f', -1, 64)
}

func rpm(v int) string {
	if v <= 0 {
		return "none"
	}
	return strconv.Itoa(v)
}

// providerChange describes a provider save. The key and the proxy are
// secrets — a proxy address carries a password as often as not — so the
// entry says that they changed and never what to.
func providerChange(before *storage.ProviderConfig, after storage.ProviderConfig) map[string]any {
	var b storage.ProviderConfig
	if before != nil {
		b = *before
	}
	var changes []string
	// A built-in is named after its kind, so saying it again says nothing.
	if (before == nil && !after.Kind.Builtin()) || (before != nil && b.Kind != after.Kind) {
		changes = append(changes, "kind: "+string(after.Kind))
	}
	if b.BaseURL != after.BaseURL {
		changes = append(changes, "address: "+orDefault(plainURL(b.BaseURL))+" → "+orDefault(plainURL(after.BaseURL)))
	}
	if b.APIKey != after.APIKey {
		changes = append(changes, "API key "+secretChange(b.APIKey, after.APIKey))
	}
	if b.ProxyURL != after.ProxyURL {
		changes = append(changes, "proxy "+secretChange(b.ProxyURL, after.ProxyURL))
	}
	if !slices.Equal(b.Prefixes, after.Prefixes) {
		changes = append(changes, "prefixes: "+strings.Join(after.Prefixes, ", "))
	}
	if b.TimeoutMS != after.TimeoutMS {
		changes = append(changes, "timeout: "+ms(b.TimeoutMS)+" → "+ms(after.TimeoutMS))
	}
	// A new provider is on unless somebody said otherwise.
	if (before == nil && !after.Enabled) || (before != nil && b.Enabled != after.Enabled) {
		changes = append(changes, "enabled: "+strconv.FormatBool(after.Enabled))
	}
	if len(changes) == 0 {
		return nil
	}
	return map[string]any{"changes": changes}
}

func secretChange(before, after string) string {
	switch {
	case before == "":
		return "set"
	case after == "":
		return "removed"
	default:
		return "replaced"
	}
}

// plainURL drops a query string, which is where the odd provider takes its
// key; an address has no user info by then, buildProvider refuses it.
func plainURL(s string) string {
	base, _, _ := strings.Cut(s, "?")
	return base
}

func orDefault(s string) string {
	if s == "" {
		return "default"
	}
	return s
}

func ms(v int) string {
	if v <= 0 {
		return "default"
	}
	return strconv.Itoa(v) + "ms"
}

// alertsChange describes an alert settings save. The webhook address carries
// its secret in the path — a Slack hook, a Telegram bot token — so the entry
// names only where it points, as the console shows it: scheme and host.
func alertsChange(before, after alerter.Config, urlSent bool) map[string]any {
	var changes []string
	switch {
	case before.URL == "" && after.URL != "":
		changes = append(changes, "destination set: "+after.URL)
	case before.URL != "" && after.URL == "":
		changes = append(changes, "destination removed")
	case urlSent && after.URL != "":
		changes = append(changes, "destination replaced: "+after.URL)
	}
	if before.Format != after.Format && after.URL != "" {
		changes = append(changes, "format: "+string(after.Format))
	}
	if b, a := alertActions(before.Actions), alertActions(after.Actions); !slices.Equal(b, a) {
		changes = append(changes, "alert on: "+strings.Join(a, ", "))
	}
	if before.ChatID != after.ChatID {
		changes = append(changes, "chat: "+orDefault(after.ChatID))
	}
	if before.DebounceSeconds != after.DebounceSeconds {
		changes = append(changes, "repeat alerts after: "+strconv.Itoa(after.DebounceSeconds)+"s")
	}
	if len(changes) == 0 {
		return nil
	}
	return map[string]any{"changes": changes}
}

// alertActions is the list as the alerter reads it: none means blocked only.
func alertActions(in []string) []string {
	if len(in) == 0 {
		return []string{"blocked"}
	}
	out := slices.Clone(in)
	slices.Sort(out)
	return out
}
