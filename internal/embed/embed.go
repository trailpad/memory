package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		return nil, fmt.Errorf("embedding response status %d", resp.StatusCode)
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
