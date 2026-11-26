package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// probeClient waits longer than httpClient: the first embedding request may
// load the model on the server.
var probeClient = &http.Client{Timeout: 60 * time.Second}

// ValidateBaseURL checks an http(s) URL with a host, as entered for Ollama
// or GPUStack, and returns it without a trailing slash.
func ValidateBaseURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("%q ist keine gültige Adresse (http:// oder https:// mit Host)", raw)
	}
	if u.User != nil {
		return "", errors.New("Zugangsdaten gehören nicht in die Adresse, bitte das Feld für den API-Schlüssel nutzen")
	}
	return strings.TrimRight(u.String(), "/"), nil
}

// IsLocal reports whether a base URL points at this machine. Anything else,
// a server in the LAN included, receives document excerpts over the network.
func IsLocal(base string) bool {
	u, err := url.Parse(base)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// Host returns the host[:port] of a base URL, for display and consent.
func Host(base string) string {
	if u, err := url.Parse(base); err == nil {
		return u.Host
	}
	return base
}

// GPUStackModels lists the models an OpenAI-compatible server (GPUStack,
// LM Studio, llama.cpp, …) offers under base, e.g. https://gpu.example/v1.
func GPUStackModels(ctx context.Context, base, apiKey string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/models", nil)
	if err != nil {
		return nil, err
	}
	authorize(req, apiKey)
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nicht erreichbar: %w", err)
	}
	defer res.Body.Close()
	if err := statusError(res); err != nil {
		return nil, err
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("unerwartete Antwort von %s/models: %w", base, err)
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			out = append(out, m.ID)
		}
	}
	return out, nil
}

// GPUStackEmbeddingDim embeds a short text with model and returns the vector
// length; this also proves the model is an embedding model.
func GPUStackEmbeddingDim(ctx context.Context, base, apiKey, model string) (int, error) {
	payload, _ := json.Marshal(map[string]any{"model": model, "input": "Probe"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req, apiKey)
	res, err := probeClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("nicht erreichbar: %w", err)
	}
	defer res.Body.Close()
	if err := statusError(res); err != nil {
		return 0, fmt.Errorf("%s ist kein Embedding-Modell oder nicht verfügbar: %w", model, err)
	}
	var body struct {
		Data []struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil || len(body.Data) == 0 || len(body.Data[0].Embedding) == 0 {
		return 0, fmt.Errorf("%s lieferte keinen Vektor", model)
	}
	return len(body.Data[0].Embedding), nil
}

// LooksLikeEmbedding guesses from a model name; GPUStack's model list does
// not say what a model is for.
func LooksLikeEmbedding(name string) bool {
	n := strings.ToLower(name)
	for _, k := range []string{"embed", "bge-", "bge_", "e5-", "gte-", "minilm", "jina-emb", "nomic"} {
		if strings.Contains(n, k) {
			return !strings.Contains(n, "rerank")
		}
	}
	return false
}

func authorize(req *http.Request, apiKey string) {
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
}

func statusError(res *http.Response) error {
	switch {
	case res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden:
		return errors.New("API-Schlüssel abgelehnt")
	case res.StatusCode >= 300:
		return fmt.Errorf("Server antwortet mit %s", res.Status)
	}
	return nil
}
