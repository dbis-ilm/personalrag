package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"
)

// OllamaModel is one installed model.
type OllamaModel struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Embedding bool   `json:"embedding"`
}

var httpClient = &http.Client{Timeout: 5 * time.Second}

// OllamaModels lists installed models; an error means Ollama is unreachable.
func OllamaModels(ctx context.Context, host string) ([]OllamaModel, error) {
	var resp struct {
		Models []struct {
			Name         string   `json:"name"`
			Size         int64    `json:"size"`
			Capabilities []string `json:"capabilities"`
			Details      struct {
				Family string `json:"family"`
			} `json:"details"`
		} `json:"models"`
	}
	if err := getJSON(ctx, strings.TrimRight(host, "/")+"/api/tags", &resp); err != nil {
		return nil, err
	}
	out := make([]OllamaModel, 0, len(resp.Models))
	for _, m := range resp.Models {
		emb := slices.Contains(m.Capabilities, "embedding") ||
			(len(m.Capabilities) == 0 && (strings.Contains(m.Name, "embed") || m.Details.Family == "bert"))
		out = append(out, OllamaModel{Name: m.Name, Size: m.Size, Embedding: emb})
	}
	return out, nil
}

// EmbeddingDim asks Ollama for a model's embedding length.
func EmbeddingDim(ctx context.Context, host, model string) (int, error) {
	body := strings.NewReader(fmt.Sprintf(`{"model":%q}`, model))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(host, "/")+"/api/show", body)
	if err != nil {
		return 0, err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("ollama /api/show: %s", res.Status)
	}
	var info struct {
		ModelInfo map[string]any `json:"model_info"`
	}
	if err := json.NewDecoder(res.Body).Decode(&info); err != nil {
		return 0, err
	}
	for k, v := range info.ModelInfo {
		if strings.HasSuffix(k, ".embedding_length") {
			if f, ok := v.(float64); ok {
				return int(f), nil
			}
		}
	}
	return 0, fmt.Errorf("embedding length of %s unknown", model)
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, res.Status)
	}
	return json.NewDecoder(res.Body).Decode(v)
}
