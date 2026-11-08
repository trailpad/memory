package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Embedder interface {
	Model() string
	Dimensions() int
	Embed(context.Context, []string) ([][]float32, error)
}

type Disabled struct{}

func (Disabled) Model() string   { return "" }
func (Disabled) Dimensions() int { return 0 }
func (Disabled) Embed(context.Context, []string) ([][]float32, error) {
	return nil, errors.New("embeddings disabled")
}

type ResponseError struct {
	Status  int
	Excerpt string
}

func (e *ResponseError) Error() string {
	return fmt.Sprintf("embedding response status %d: %s", e.Status, e.Excerpt)
}

type HTTP struct {
	URL, Key, ModelName string
	Size                int
	Client              *http.Client
}

func (h *HTTP) Model() string   { return h.ModelName }
func (h *HTTP) Dimensions() int { return h.Size }
func (h *HTTP) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	body, err := json.Marshal(struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}{h.ModelName, texts, h.Size})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(h.URL, "/")+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("invalid embedding endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.Key)
	client := h.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("embedding request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		excerpt := string(raw)
		if len(raw) == 64<<10 {
			excerpt = "provider response exceeded excerpt read limit"
		}
		for _, secret := range append([]string{h.Key}, texts...) {
			if secret != "" {
				encoded, _ := json.Marshal(secret)
				excerpt = strings.ReplaceAll(excerpt, string(encoded[1:len(encoded)-1]), "[redacted]")
				excerpt = strings.ReplaceAll(excerpt, secret, "[redacted]")
			}
		}
		if len(excerpt) > 300 {
			excerpt = excerpt[:300]
		}
		return nil, &ResponseError{Status: resp.StatusCode, Excerpt: excerpt}
	}
	var result struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 32<<20)).Decode(&result); err != nil {
		return nil, errors.New("invalid embedding response")
	}
	if len(result.Data) != len(texts) {
		return nil, errors.New("embedding count mismatch")
	}
	vectors := make([][]float32, len(texts))
	for i, row := range result.Data {
		if len(row.Embedding) != h.Size {
			return nil, fmt.Errorf("embedding dimensions: got %d, expected %d", len(row.Embedding), h.Size)
		}
		vectors[i] = row.Embedding
	}
	return vectors, nil
}
