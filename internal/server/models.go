package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/danilovid/aperture/internal/storage"
)

// GET /v1/models: every model this key can actually use, asked of each
// provider it has a credential for — not a list somebody typed in, which is
// out of date the week a provider ships something.
//
// A provider's list is only half the answer, though. The gateway routes by
// model name, so a model is offered only when a request for it would come back
// to the provider that listed it: Groq serves "openai/gpt-oss-120b", but a
// request for that name goes to OpenAI, and offering it would be offering an
// error.

// modelsTimeout bounds one provider's answer; a slow one is left out rather
// than holding up the rest.
const modelsTimeout = 10 * time.Second

// modelsCacheTTL is how long a provider's list is reused. Lists change on the
// scale of weeks; the playground asks every time it opens.
const modelsCacheTTL = 10 * time.Minute

// modelEntry is one model in the OpenAI list shape.
type modelEntry struct {
	ID          string `json:"id"`
	Object      string `json:"object"`
	Created     int64  `json:"created,omitempty"`
	OwnedBy     string `json:"owned_by"`
	DisplayName string `json:"display_name,omitempty"`
}

// modelSource is a provider a key might send requests to.
type modelSource struct {
	name string
	kind storage.ProviderKind
	cfg  *storage.ProviderConfig
}

type modelCache struct {
	mu      sync.Mutex
	entries map[string]modelCacheEntry
}

type modelCacheEntry struct {
	models  []modelEntry
	expires time.Time
}

// modelSources lists the providers in the order the list is shown: the
// built-ins that speak chat, then the organization's own.
func (h *Handlers) modelSources(ctx context.Context, orgID string) []modelSource {
	list := h.orgProviders(ctx, orgID)
	var out []modelSource
	for _, k := range []storage.ProviderKind{storage.KindOpenAI, storage.KindAnthropic, storage.KindGroq} {
		out = append(out, modelSource{name: string(k), kind: k, cfg: builtin(list, k)})
	}
	for i := range list {
		if list[i].Kind == storage.KindCompatible {
			out = append(out, modelSource{name: list[i].Name, kind: storage.KindCompatible, cfg: &list[i]})
		}
	}
	if h.ProviderStore == nil {
		for _, cp := range h.CustomProviders {
			out = append(out, modelSource{name: cp.Name, kind: storage.KindCompatible})
		}
	}
	return out
}

func (h *Handlers) handleModels(w http.ResponseWriter, r *http.Request) {
	key, err := h.resolveKey(r)
	if err != nil {
		h.writeAuthError(w, err)
		return
	}
	ctx := r.Context()
	sources := h.modelSources(ctx, key.OrgID)

	type result struct {
		asked  bool
		models []modelEntry
		err    error
	}
	results := make([]result, len(sources))
	var wg sync.WaitGroup
	for i, src := range sources {
		u, err := h.buildUpstream(key, src.name, src.kind, src.cfg)
		if err != nil {
			// No key, switched off, or no address: nothing this key can
			// send there, so nothing to list.
			continue
		}
		results[i].asked = true
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].models, results[i].err = h.providerModels(ctx, u)
		}()
	}
	wg.Wait()

	data := []modelEntry{}
	var unavailable []map[string]string
	asked := 0
	for i, res := range results {
		if !res.asked {
			continue
		}
		asked++
		name := sources[i].name
		if res.err != nil {
			h.Logger.Warn("listing models failed", "provider", name, "org", key.OrgID, "err", res.err)
			unavailable = append(unavailable, map[string]string{"provider": name, "error": res.err.Error()})
			continue
		}
		for _, m := range res.models {
			if routed, _, _ := h.route(ctx, key.OrgID, m.ID); routed != name {
				continue
			}
			m.Object, m.OwnedBy = "model", name
			data = append(data, m)
		}
	}

	if asked == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error": "no API key configured for any provider. Add one under Providers in the console.",
		})
		return
	}
	out := map[string]any{"object": "list", "data": data}
	if len(unavailable) > 0 {
		// Not part of OpenAI's shape, and ignored by clients that expect it;
		// the console uses it to say why a provider's models are missing.
		out["unavailable"] = unavailable
	}
	status := http.StatusOK
	if len(data) == 0 && len(unavailable) > 0 {
		status = http.StatusBadGateway
		out["error"] = "no provider could list its models"
	}
	writeJSON(w, status, out)
}

// providerModels asks one provider for its models, newest first, or answers
// from what it said recently. Only answers are cached, not failures: a key
// fixed a minute ago should not wait ten more to be believed.
func (h *Handlers) providerModels(ctx context.Context, u *upstream) ([]modelEntry, error) {
	sum := sha256.Sum256([]byte(u.APIKey))
	cacheKey := u.Name + "|" + string(u.Kind) + "|" + u.BaseURL + "|" + hex.EncodeToString(sum[:])

	h.models.mu.Lock()
	if e, ok := h.models.entries[cacheKey]; ok && time.Now().Before(e.expires) {
		h.models.mu.Unlock()
		return e.models, nil
	}
	h.models.mu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, modelsTimeout)
	defer cancel()
	body, _, status, err := chatProvider(u).Models(ctx)
	if err != nil {
		return nil, fmt.Errorf("%s did not answer: %w", u.Name, err)
	}
	defer body.Close()
	if status != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(body, 64<<10))
		return nil, fmt.Errorf("%s answered HTTP %d%s", u.Name, status, statusHint(status))
	}
	var list struct {
		Data []modelEntry `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(body, 8<<20)).Decode(&list); err != nil {
		return nil, fmt.Errorf("%s sent a models list that is not JSON: %w", u.Name, err)
	}
	models := list.Data[:0]
	for _, m := range list.Data {
		if m.ID != "" {
			models = append(models, m)
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		if models[i].Created != models[j].Created {
			return models[i].Created > models[j].Created
		}
		return models[i].ID < models[j].ID
	})

	h.models.mu.Lock()
	if h.models.entries == nil {
		h.models.entries = map[string]modelCacheEntry{}
	}
	h.models.entries[cacheKey] = modelCacheEntry{models: models, expires: time.Now().Add(modelsCacheTTL)}
	h.models.mu.Unlock()
	return models, nil
}

func statusHint(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return " — check the provider's API key"
	case http.StatusNotFound:
		return " — check the provider's address"
	}
	return ""
}
