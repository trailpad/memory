package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
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
