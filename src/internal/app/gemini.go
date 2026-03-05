package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type geminiClient struct {
	mu    sync.Mutex
	keys  []string
	index int
	load  sync.Once
}

var gClient geminiClient

func (g *geminiClient) loadKeys() {
	g.load.Do(func() {
		seen := map[string]struct{}{}
		add := func(k string) {
			k = strings.TrimSpace(k)
			if k == "" {
				return
			}
			if _, ok := seen[k]; ok {
				return
			}
			seen[k] = struct{}{}
			g.keys = append(g.keys, k)
		}

		add(os.Getenv("GEMINI_API_KEY"))
		add(os.Getenv("API_KEY"))

		for _, p := range []string{filepath.Join(projectRoot, "gemini_api_key.txt"), filepath.Join(projectRoot, ".gemini_api_key")} {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(b), "\n") {
				add(line)
			}
		}
	})
}

func hasGeminiConfigured() bool {
	gClient.loadKeys()
	return len(gClient.keys) > 0
}

func refineTranscript(rawText, description string) (string, error) {
	gClient.loadKeys()
	if len(gClient.keys) == 0 {
		return "", errors.New("Gemini API is not configured")
	}

	prompt := "[원본 전사문]\n\"\"\"\n" + rawText + "\n\"\"\"\n\n"
	if strings.TrimSpace(description) != "" {
		prompt += "[설명]\n\"\"\"\n" + description + "\n\"\"\"\n\n"
	}

	gClient.mu.Lock()
	start := gClient.index
	gClient.mu.Unlock()

	var lastErr error
	for i := 0; i < len(gClient.keys); i++ {
		idx := (start + i) % len(gClient.keys)
		text, err := geminiGenerate(gClient.keys[idx], prompt)
		if err == nil && strings.TrimSpace(text) != "" {
			gClient.mu.Lock()
			gClient.index = (idx + 1) % len(gClient.keys)
			gClient.mu.Unlock()
			return strings.TrimSpace(text), nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("empty response")
	}
	return "", lastErr
}

func geminiGenerate(apiKey, prompt string) (string, error) {
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", geminiModel, apiKey)
	payload := map[string]any{
		"system_instruction": map[string]any{"parts": []map[string]string{{"text": baseInstructions}}},
		"contents":           []map[string]any{{"parts": []map[string]string{{"text": prompt}}}},
		"generationConfig":   map[string]any{"temperature": 0.8},
	}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("gemini error: %s", string(respBody))
	}

	var parsed struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", err
	}
	if len(parsed.Candidates) == 0 {
		return "", errors.New("no candidates")
	}
	var out strings.Builder
	for _, p := range parsed.Candidates[0].Content.Parts {
		out.WriteString(p.Text)
	}
	return out.String(), nil
}
