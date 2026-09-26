package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mutegate/mutegate/internal/config"
	"github.com/mutegate/mutegate/internal/inspector"
	"github.com/mutegate/mutegate/internal/storage"
)

// Two organizations on one gateway must never see each other's traffic. The
// stores enforce that on their own (internal/storage/storagetest); what is
// tested here is the layer above — that every console endpoint asks for the
// organization on the caller's session, and that nothing a caller sends can
// change which one they get.

// orgLogStore is a request log that insists on being asked properly: an empty
// organization fails the test rather than quietly returning everything, which
// is exactly how this class of bug reaches production.
type orgLogStore struct {
	t       *testing.T
	entries []storage.LogEntry
}

func (s *orgLogStore) requireOrg(orgID string) {
	s.t.Helper()
	if orgID == "" {
		s.t.Error("the request log was queried without an organization")
	}
}

func (s *orgLogStore) of(orgID string) []storage.LogEntry {
	s.requireOrg(orgID)
	var out []storage.LogEntry
	for _, e := range s.entries {
		if e.OrgID == orgID {
			out = append(out, e)
		}
	}
	return out
}

func (s *orgLogStore) Insert(_ context.Context, e storage.LogEntry) error {
	s.entries = append(s.entries, e)
	return nil
}

func (s *orgLogStore) List(_ context.Context, orgID string, _ storage.LogFilter) ([]storage.LogEntry, error) {
	return s.of(orgID), nil
}

func (s *orgLogStore) Summary(_ context.Context, orgID string, _ time.Time) (storage.StatsSummary, error) {
	sum := storage.StatsSummary{}
	for _, e := range s.of(orgID) {
		sum.Requests++
		sum.TotalTokens += int64(e.TotalTokens)
		sum.CostUSD += e.CostUSD
	}
	return sum, nil
}

func (s *orgLogStore) CostSince(_ context.Context, orgID, keyID string, _ time.Time) (float64, error) {
	var total float64
	for _, e := range s.of(orgID) {
		if e.KeyID == keyID {
			total += e.CostUSD
		}
	}
	return total, nil
}

func (s *orgLogStore) Timeseries(_ context.Context, orgID string, _ time.Time, _ int) ([]storage.TimeseriesBucket, error) {
	entries := s.of(orgID)
	if len(entries) == 0 {
		return nil, nil
	}
	return []storage.TimeseriesBucket{{Ts: entries[0].Ts, Requests: int64(len(entries))}}, nil
}

func (s *orgLogStore) ModelStats(_ context.Context, orgID string, _ time.Time) ([]storage.ModelStat, error) {
	byModel := map[string]*storage.ModelStat{}
	var out []storage.ModelStat
	for _, e := range s.of(orgID) {
		m := byModel[e.Model]
		if m == nil {
			m = &storage.ModelStat{Model: e.Model, Provider: e.Provider}
			byModel[e.Model] = m
		}
		m.Requests++
	}
	for _, m := range byModel {
		out = append(out, *m)
	}
	return out, nil
}

// twoOrganizations builds a gateway with two organizations, each with an owner
// signed in, and one incident and one request logged in each.
func twoOrganizations(t *testing.T) (acme, globex *client, dlp *storage.MemDLPStore, logs *orgLogStore) {
	t.Helper()
	accounts := storage.NewMemAccountStore()
	dlp = storage.NewMemDLPStore(100)
	logs = &orgLogStore{t: t}
	h := Routes(Options{
		KeyStore:     config.NewRuntimeStore("ap-test").KeyStore(),
		AccountStore: accounts,
		DLPStore:     dlp,
		LogStore:     logs,
		PolicyStore:  storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionBlock}),
		AdminAPIKey:  "instance-admin",
		Logger:       slog.Default(),
	})

	signIn := func(org, email string) (*client, string) {
		token := bootstrap(t, h, org, email)
		c := newClient(t, h)
		rec := c.do(http.MethodPost, "/api/auth/register",
			`{"token":"`+token+`","email":"`+email+`","name":"Owner","password":"a good long password"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("register in %s = %d: %s", org, rec.Code, rec.Body.String())
		}
		me := c.me(rec)
		if me.Organization == nil {
			t.Fatalf("no organization after registering in %s", org)
		}
		return c, me.Organization.ID
	}

	acme, acmeID := signIn("Acme", "owner@acme.test")
	globex, globexID := signIn("Globex", "owner@globex.test")
	if acmeID == globexID {
		t.Fatal("the two organizations came out the same")
	}

	seed := func(orgID, rule, model string) {
		if err := dlp.Insert(context.Background(), storage.DLPEvent{
			OrgID: orgID, Ts: time.Now(), KeyID: "k", Model: model, Provider: "openai",
			Rule: rule, Group: "secrets", Action: "blocked", MaskedSample: "***",
		}); err != nil {
			t.Fatalf("seed an event: %v", err)
		}
		logs.Insert(context.Background(), storage.LogEntry{
			OrgID: orgID, Ts: time.Now(), Model: model, Provider: "openai",
			TotalTokens: 100, CostUSD: 1, KeyID: "k", StatusCode: 200,
		})
	}
	seed(acmeID, "acme-rule", "acme-model")
	seed(globexID, "globex-rule", "globex-model")

	// Remember which organization each client is in, for the header test.
	acme.orgID, globex.orgID = acmeID, globexID
	return acme, globex, dlp, logs
}

func TestConsoleShowsOnlyTheCallersOrganization(t *testing.T) {
	acme, globex, _, _ := twoOrganizations(t)

	// Each endpoint is asked for by both owners; neither may see the other's
	// data anywhere in the answer.
	for _, path := range []string{
		"/admin/dlp/events",
		"/admin/dlp/report?period=7d",
		"/admin/stats/logs",
		"/admin/stats/models",
	} {
		t.Run(path, func(t *testing.T) {
			mine := acme.do(http.MethodGet, path, "")
			if mine.Code != http.StatusOK {
				t.Fatalf("%s = %d: %s", path, mine.Code, mine.Body.String())
			}
			body := mine.Body.String()
			if !strings.Contains(body, "acme") {
				t.Errorf("%s does not show the caller's own data: %s", path, body)
			}
			if strings.Contains(body, "globex") {
				t.Errorf("%s leaked another organization's data: %s", path, body)
			}

			theirs := globex.do(http.MethodGet, path, "")
			if strings.Contains(theirs.Body.String(), "acme") {
				t.Errorf("%s leaked in the other direction: %s", path, theirs.Body.String())
			}
		})
	}
}

func TestSummaryCountsOnlyTheCallersOrganization(t *testing.T) {
	acme, _, _, _ := twoOrganizations(t)

	rec := acme.do(http.MethodGet, "/admin/stats/summary", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("summary = %d: %s", rec.Code, rec.Body.String())
	}
	var sum storage.StatsSummary
	json.Unmarshal(rec.Body.Bytes(), &sum)
	if sum.Requests != 1 {
		t.Errorf("summary counts %d requests; each organization logged one", sum.Requests)
	}

	dlpRec := acme.do(http.MethodGet, "/admin/dlp/summary", "")
	var dlpSum storage.DLPSummary
	json.Unmarshal(dlpRec.Body.Bytes(), &dlpSum)
	if dlpSum.Total != 1 || dlpSum.Blocked != 1 {
		t.Errorf("incident counts = %+v; each organization had one blocked event", dlpSum)
	}
}

// The organization comes from the session. X-Mutegate-Org exists for the
// operator's key, which belongs to no organization — a signed-in person
// sending it must be ignored, not obeyed.
func TestSessionCallerCannotPickAnotherOrganizationByHeader(t *testing.T) {
	acme, globex, _, _ := twoOrganizations(t)

	acme.headers = map[string]string{"X-Mutegate-Org": globex.orgID}
	rec := acme.do(http.MethodGet, "/admin/dlp/events", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("events = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "globex") {
		t.Fatalf("a header chose another organization's data: %s", rec.Body.String())
	}
}

// Switching organizations is allowed — but only into one the person belongs
// to, and the store is what refuses the rest.
func TestSwitchingIntoAnotherOrganizationIsRefused(t *testing.T) {
	acme, globex, _, _ := twoOrganizations(t)

	rec := acme.do(http.MethodPost, "/api/auth/switch-org", `{"org_id":"`+globex.orgID+`"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("switching into a stranger's organization = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	// And the session is unchanged: the caller still sees their own data.
	events := acme.do(http.MethodGet, "/admin/dlp/events", "")
	if strings.Contains(events.Body.String(), "globex") {
		t.Errorf("a refused switch still moved the session: %s", events.Body.String())
	}
}

// twoOrgKeyStore hands out one Mutegate key per organization, which is what
// the traffic path uses to decide where a request's rows belong.
type twoOrgKeyStore struct{ byToken map[string]*storage.Key }

func (s *twoOrgKeyStore) GetByMutegateKey(_ context.Context, token string) (*storage.Key, error) {
	if k, ok := s.byToken[token]; ok {
		return k, nil
	}
	return nil, storage.ErrKeyNotFound
}

func (s *twoOrgKeyStore) Create(context.Context, string, string, string, map[string]string) (*storage.Key, error) {
	return nil, storage.ErrNotSupported
}
func (s *twoOrgKeyStore) List(context.Context, string) ([]storage.Key, error) { return nil, nil }
func (s *twoOrgKeyStore) Delete(context.Context, string, string) error {
	return storage.ErrNotSupported
}
func (s *twoOrgKeyStore) SetProviderKeys(context.Context, string, map[string]string) error {
	return nil
}
func (s *twoOrgKeyStore) GetProviderKeys(context.Context, string) (map[string]string, error) {
	return nil, nil
}
func (s *twoOrgKeyStore) ClearProviderKeys(context.Context, string) error { return nil }

// Reading is only half of isolation. A request carries no organization of its
// own — the key does — so the rows it writes have to land in that key's
// organization, or one tenant's traffic quietly becomes another's history.
func TestTrafficIsRecordedUnderTheKeysOrganization(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer upstream.Close()

	const orgA = "11111111-1111-1111-1111-111111111111"
	const orgB = "22222222-2222-2222-2222-222222222222"
	keys := &twoOrgKeyStore{byToken: map[string]*storage.Key{
		"ap-a": {ID: "key-a", OrgID: orgA, Name: "acme", Providers: map[string]string{"openai": "sk-x"}},
		"ap-b": {ID: "key-b", OrgID: orgB, Name: "globex", Providers: map[string]string{"openai": "sk-x"}},
	}}
	dlp := storage.NewMemDLPStore(100)
	logs := &orgLogStore{t: t}
	h := Routes(Options{
		KeyStore:      keys,
		DLPStore:      dlp,
		LogStore:      logs,
		PolicyStore:   storage.NewMemPolicyStore(inspector.Policy{Secrets: inspector.ActionAlert}),
		Inspector:     inspector.New(),
		OpenAIBaseURL: upstream.URL,
		AdminAPIKey:   "instance-admin",
		Logger:        slog.Default(),
	})

	send := func(token, secret string) {
		t.Helper()
		body := `{"model":"gpt-4o-mini","messages":[{"role":"user","content":"my key is ` + secret + `"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("proxy with %s = %d: %s", token, rec.Code, rec.Body.String())
		}
	}
	send("ap-a", "AKIAIOSFODNN7EXAMPLE")
	send("ap-b", "AKIAIOSFODNN7EXAMPLE")

	ctx := context.Background()
	for _, org := range []string{orgA, orgB} {
		events, err := dlp.List(ctx, org, storage.DLPFilter{})
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		if len(events) != 1 {
			t.Errorf("organization %s has %d incidents, want exactly its own one", org[:8], len(events))
		}
		entries, err := logs.List(ctx, org, storage.LogFilter{})
		if err != nil {
			t.Fatalf("list logs: %v", err)
		}
		if len(entries) != 1 {
			t.Errorf("organization %s has %d log rows, want exactly its own one", org[:8], len(entries))
		}
	}
}
