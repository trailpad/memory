package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestCapturedExchange(t *testing.T) {
	request, err := os.ReadFile("testdata/gemini-768-request.json")
	if err != nil {
		t.Fatal(err)
	}
	response, err := os.ReadFile("testdata/gemini-768-response.json")
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.Unmarshal(request, &captured); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/embeddings" || r.Header.Get("Authorization") != "Bearer test-placeholder" {
			t.Error("request headers or path")
		}
		var actual, expected any
		if err := json.NewDecoder(r.Body).Decode(&actual); err != nil {
			t.Error(err)
		}
		if err := json.Unmarshal(request, &expected); err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Error("request differs from capture")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer server.Close()
	h := &HTTP{URL: server.URL, Key: "test-placeholder", ModelName: captured.Model, Size: captured.Dimensions}
	vectors, err := h.Embed(context.Background(), captured.Input)
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 3 || len(vectors[0]) != 768 {
		t.Fatal("incorrect vector shape")
	}
}

func TestCapturedResponseShapeValidation(t *testing.T) {
	request, err := os.ReadFile("testdata/gemini-768-request.json")
	if err != nil {
		t.Fatal(err)
	}
	response, err := os.ReadFile("testdata/gemini-768-response.json")
	if err != nil {
		t.Fatal(err)
	}
	var captured struct {
		Model      string   `json:"model"`
		Input      []string `json:"input"`
		Dimensions int      `json:"dimensions"`
	}
	if err := json.Unmarshal(request, &captured); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(response) }))
	defer server.Close()
	for _, test := range []struct {
		name  string
		size  int
		texts []string
	}{{"count", captured.Dimensions, captured.Input[:2]}, {"dimensions", captured.Dimensions + 1, captured.Input}} {
		t.Run(test.name, func(t *testing.T) {
			h := &HTTP{URL: server.URL, ModelName: captured.Model, Size: test.size}
			if _, err := h.Embed(t.Context(), test.texts); err == nil {
				t.Fatal("shape mismatch accepted")
			}
		})
	}
}
