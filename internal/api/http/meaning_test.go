package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	httpapi "github.com/trailpad/memory/internal/api/http"
	"github.com/trailpad/memory/internal/embed"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/trailpad/memory/internal/recall"
	"github.com/trailpad/memory/internal/store"
)

type capturedEmbedder struct {
	texts   []string
	vectors [][]float32
	fail    bool
}

func (c *capturedEmbedder) Model() string   { return "gemini-embedding-001" }
func (c *capturedEmbedder) Dimensions() int { return 768 }
func (c *capturedEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c.fail {
		return nil, errors.New("provider unavailable")
	}
	out := make([][]float32, len(texts))
	for i, text := range texts {
		for j, captured := range c.texts {
			if text == captured {
				out[i] = c.vectors[j]
			}
		}
		if out[i] == nil {
			return nil, errors.New("text absent from capture")
		}
	}
	return out, nil
}
func loadCapturedEmbedder(t *testing.T) *capturedEmbedder {
	t.Helper()
	request, err := os.ReadFile("../../embed/testdata/gemini-768-request.json")
	if err != nil {
		t.Fatal(err)
	}
	response, err := os.ReadFile("../../embed/testdata/gemini-768-response.json")
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Input []string `json:"input"`
	}
	var resp struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(request, &req); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(response, &resp); err != nil {
		t.Fatal(err)
	}
	c := &capturedEmbedder{texts: req.Input}
	for _, r := range resp.Data {
		c.vectors = append(c.vectors, r.Embedding)
	}
	return c
}
func TestMeaningThroughHTTP(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			e := newEngine(t)
			provider := loadCapturedEmbedder(t)
			var index store.VectorIndex = store.NewMemoryVectorIndex()
			if backend == "postgres" {
				var err error
				index, err = e.store.NewVectorIndex(t.Context(), 768)
				if err != nil {
					t.Fatal(err)
				}
			}
			e.recall.Embedder = provider
			e.recall.Vectors = index
			bucket := e.repository("repo_meaning", "ev_seed", provider.texts[2])
			events := []map[string]any{}
			for i, id := range []string{"ev_auth_a", "ev_auth_b", "ev_catalogue"} {
				events = append(events, map[string]any{"id": id, "bucket": bucket, "type": "observation", "occurred_at": "2026-10-21T10:00:00Z", "content": []map[string]string{{"text": provider.texts[i]}}})
				if i == 0 {
					delete(events[len(events)-1], "content")
					events[len(events)-1]["title"] = provider.texts[i]
				}
			}
			e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": events})
			e.readers(bucket, "gh_ana")
			token := e.sign(e.claims("user_ana", "gh_ana"))
			if err := e.store.PrepareEmbeddings(t.Context(), provider, false); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.EmbedBatch(t.Context(), provider, index); err != nil {
				t.Fatal(err)
			}
			raw := e.must(200, "POST", "/v1/events/links", e.key, token, map[string]any{"buckets": []string{bucket}, "from": "2026-10-21T00:00:00Z", "to": "2026-10-22T00:00:00Z", "approaches": []string{"meaning"}})
			var linked struct {
				Links []recall.EventLink `json:"links"`
			}
			if err := json.Unmarshal(raw, &linked); err != nil {
				t.Fatal(err)
			}
			if len(linked.Links) != 1 || linked.Links[0].From != "ev_auth_a" || linked.Links[0].To != "ev_auth_b" || linked.Links[0].Because["meaning"].Model != provider.Model() {
				t.Fatalf("meaning links: %s", raw)
			}
			raw = e.must(200, "POST", "/v1/recall", e.key, token, map[string]any{"context": map[string]string{"text": provider.texts[0]}, "filters": map[string]any{"buckets": []string{bucket}}, "explain": true})
			var recalled recall.Response
			if err := json.Unmarshal(raw, &recalled); err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, r := range recalled.Results {
				if r.ID == "ev_auth_b" && r.Because.Meaning != nil && r.Because.Meaning.Score > 0 {
					seen = true
				}
			}
			if !seen {
				t.Fatalf("missing semantic recall: %s", raw)
			}
			provider.fail = true
			raw = e.must(200, "POST", "/v1/recall", e.key, token, map[string]any{"context": map[string]string{"text": provider.texts[0]}})
			if err := json.Unmarshal(raw, &recalled); err != nil {
				t.Fatal(err)
			}
			if len(recalled.Skipped) != 1 || recalled.Skipped[0] != "meaning" {
				t.Fatalf("provider failure: %s", raw)
			}
			provider.fail = false
			_ = e.repository("repo_hidden", "ev_hidden", provider.texts[0])
			e.recall.Vectors = untrustedIndex{VectorIndex: index, id: "ev_hidden"}
			raw = e.must(200, "POST", "/v1/recall", e.key, token, map[string]any{"context": map[string]string{"text": provider.texts[0]}, "explain": true})
			if err := json.Unmarshal(raw, &recalled); err != nil {
				t.Fatal(err)
			}
			for _, result := range recalled.Results {
				if result.ID == "ev_hidden" {
					t.Fatal("vector backend granted access")
				}
			}

		})
	}
}

type untrustedIndex struct {
	store.VectorIndex
	id string
}

func (u untrustedIndex) Nearest(ctx context.Context, workspace, model string, vector []float32, filter store.VectorFilter) ([]store.VectorMatch, error) {
	rows, err := u.VectorIndex.Nearest(ctx, workspace, model, vector, filter)
	return append([]store.VectorMatch{{EventID: u.id, Cosine: 1}}, rows...), err
}

func TestHTTPEmbeddingRecovery(t *testing.T) {
	for _, status := range []int{400, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			e := newEngine(t)
			captured := loadCapturedEmbedder(t)
			var logs bytes.Buffer
			e.store.EmbeddingLog = slog.New(slog.NewTextHandler(&logs, nil))
			calls := 0
			providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Input []string `json:"input"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				for _, text := range request.Input {
					if text == "" {
						w.WriteHeader(400)
						_, _ = w.Write([]byte(`{"error":"empty input"}`))
						return
					}
				}
				for _, text := range request.Input {
					if text == "rejected-input" {
						w.WriteHeader(status)
						_, _ = w.Write([]byte("invalid rejected-input credential-test " + strings.Repeat("x", 400)))
						return
					}
				}
				vectors, err := captured.Embed(r.Context(), request.Input)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				data := []map[string]any{}
				for _, v := range vectors {
					data = append(data, map[string]any{"embedding": v})
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer providerServer.Close()
			provider := &embed.HTTP{URL: providerServer.URL, Key: "credential-test", ModelName: captured.Model(), Size: 768}
			index := store.NewMemoryVectorIndex()
			bucket := e.repository("repo_worker", "ev_seed", captured.texts[0])
			events := []map[string]any{{"bucket": bucket, "id": "ev_empty", "type": "github.push"}, {"bucket": bucket, "id": "ev_good", "type": "observation", "title": captured.texts[1]}, {"bucket": bucket, "id": "ev_bad", "type": "observation", "title": "rejected-input"}}
			e.must(200, "POST", "/v1/records", e.key, "", map[string]any{"events": events})
			if err := e.store.PrepareEmbeddings(t.Context(), provider, false); err != nil {
				t.Fatal(err)
			}
			if _, err := e.store.EmbedBatch(t.Context(), provider, index); err == nil {
				t.Fatal("expected provider failure")
			}
			var complete bool
			var attempts int
			if err := e.store.Pool().QueryRow(t.Context(), `select complete,attempts from embedding_jobs where event_id='ev_empty'`).Scan(&complete, &attempts); err != nil {
				t.Fatal(err)
			}
			if !complete || attempts != 0 {
				t.Fatalf("empty job: %v %d", complete, attempts)
			}
			expectedFailing := 1
			if status == 503 {
				expectedFailing = 3
			}
			failing, err := e.store.EmbeddingFailing(t.Context())
			if err != nil || failing != expectedFailing {
				t.Fatalf("failing: %d %v", failing, err)
			}
			health := httptest.NewServer((&httpapi.Server{EmbeddingBacklog: e.store.EmbeddingBacklog, EmbeddingFailing: e.store.EmbeddingFailing}).Handler())
			defer health.Close()
			resp, err := http.Get(health.URL + "/healthz")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var counts map[string]int
			if err := json.NewDecoder(resp.Body).Decode(&counts); err != nil {
				t.Fatal(err)
			}
			if counts["embedding_backlog"] != expectedFailing || counts["embedding_failing"] != expectedFailing {
				t.Fatalf("health: %v", counts)
			}
			matches, err := index.Nearest(t.Context(), "ws_acme", provider.Model(), captured.vectors[1], store.VectorFilter{Buckets: []string{bucket}, K: 10})
			if err != nil {
				t.Fatal(err)
			}
			if status == 400 && (len(matches) != 2 || calls != 4) {
				t.Fatalf("isolated batch: vectors=%d calls=%d", len(matches), calls)
			}
			if status == 503 && (len(matches) != 0 || calls != 1) {
				t.Fatalf("server failure: vectors=%d calls=%d", len(matches), calls)
			}
			for _, match := range matches {
				if match.EventID == "ev_empty" {
					t.Fatal("empty event has vector")
				}
			}
			output := logs.String()
			if !strings.Contains(output, "level=WARN") || !strings.Contains(output, fmt.Sprintf("status=%d", status)) || !strings.Contains(output, "invalid") {
				t.Fatalf("missing warning: %s", output)
			}
			for _, secret := range append(captured.texts, "rejected-input", "credential-test") {
				if strings.Contains(output, secret) {
					t.Fatal("log contains request data")
				}
			}
			for i := 0; i < 6; i++ {
				if _, err := e.store.Pool().Exec(t.Context(), `update embedding_jobs set retry_at=now() where not complete`); err != nil {
					t.Fatal(err)
				}
				_, _ = e.store.EmbedBatch(t.Context(), provider, index)
			}
			if strings.Count(logs.String(), "level=WARN") != 5 {
				t.Fatalf("warning cap: %s", logs.String())
			}
		})
	}
}
