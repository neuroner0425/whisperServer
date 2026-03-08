package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

type geminiClient struct {
	mu      sync.Mutex
	clients []geminiKeyClient
	index   int
	load    sync.Once
}

type geminiKeyClient struct {
	key           string
	client        *genai.Client
	failCount     int
	cooldownUntil time.Time
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			c, err := genai.NewClient(ctx, &genai.ClientConfig{
				APIKey:  k,
				Backend: genai.BackendGeminiAPI,
			})
			if err != nil {
				procErrf("gemini.newClient", err, "api_key_suffix=%s", maskedKeySuffix(k))
				return
			}
			g.clients = append(g.clients, geminiKeyClient{key: k, client: c})
		}
		for _, k := range geminiAPIKeysFromConfig() {
			add(k)
		}
		procLogf("[GEMINI] initialized clients=%d", len(g.clients))
	})
}

func hasGeminiConfigured() bool {
	gClient.loadKeys()
	return len(gClient.clients) > 0
}

func refineTranscript(rawText, description string) (string, error) {
	gClient.loadKeys()
	gClient.mu.Lock()
	clientCount := len(gClient.clients)
	gClient.mu.Unlock()
	if clientCount == 0 {
		return "", errors.New("Gemini API is not configured")
	}

	prompt := "[원본 전사문]\n\"\"\"\n" + rawText + "\n\"\"\"\n\n"
	if strings.TrimSpace(description) != "" {
		prompt += "[설명]\n\"\"\"\n" + description + "\n\"\"\"\n\n"
	}
	fullPrompt := baseInstructions + "\n\n" + prompt

	var lastErr error = errors.New("gemini request failed")
	maxAttempts := clientCount * 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		idx, waitFor := gClient.nextReadyClient(time.Now())
		if idx < 0 {
			if waitFor > 3*time.Second {
				waitFor = 3 * time.Second
			}
			if waitFor > 0 {
				time.Sleep(waitFor)
			}
			continue
		}

		text, err := gClient.generate(idx, fullPrompt)
		if err == nil && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text), nil
		}
		lastErr = err
	}
	gClient.mu.Lock()
	for i := range gClient.clients {
		if gClient.clients[i].failCount > 0 {
			gClient.clients[i].cooldownUntil = time.Time{}
		}
	}
	gClient.mu.Unlock()
	return "", lastErr
}

func (g *geminiClient) nextReadyClient(now time.Time) (int, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.clients) == 0 {
		return -1, 0
	}

	start := g.index
	var minWait time.Duration
	for i := 0; i < len(g.clients); i++ {
		idx := (start + i) % len(g.clients)
		wait := g.clients[idx].cooldownUntil.Sub(now)
		if wait <= 0 {
			g.index = (idx + 1) % len(g.clients)
			return idx, 0
		}
		if minWait == 0 || wait < minWait {
			minWait = wait
		}
	}
	return -1, minWait
}

func (g *geminiClient) generate(idx int, prompt string) (string, error) {
	g.mu.Lock()
	if idx < 0 || idx >= len(g.clients) {
		g.mu.Unlock()
		return "", errors.New("invalid client index")
	}
	c := g.clients[idx].client
	keySuffix := maskedKeySuffix(g.clients[idx].key)
	g.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	result, err := c.Models.GenerateContent(
		ctx,
		geminiModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature: ptrFloat32(0.8),
		},
	)
	if err != nil {
		g.onFailure(idx, err)
		return "", err
	}
	if result == nil {
		err = errors.New("empty response")
		g.onFailure(idx, err)
		return "", err
	}
	text := strings.TrimSpace(result.Text())
	if text == "" {
		err = errors.New("empty response text")
		g.onFailure(idx, err)
		return "", err
	}
	g.onSuccess(idx)
	procLogf("[GEMINI] success api_key_suffix=%s", keySuffix)
	return text, nil
}

func (g *geminiClient) onSuccess(idx int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if idx < 0 || idx >= len(g.clients) {
		return
	}
	g.clients[idx].failCount = 0
	g.clients[idx].cooldownUntil = time.Time{}
}

func (g *geminiClient) onFailure(idx int, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if idx < 0 || idx >= len(g.clients) {
		return
	}

	base := 2 * time.Second
	c := &g.clients[idx]
	c.failCount++
	backoff := base * time.Duration(1<<(min(c.failCount-1, 4)))
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	if !isRetryableGeminiError(err) {
		backoff = 5 * time.Second
	}
	c.cooldownUntil = time.Now().Add(backoff)
	procErrf("gemini.generate", err, "api_key_suffix=%s cooldown=%s fail_count=%d", maskedKeySuffix(c.key), backoff, c.failCount)
}

func isRetryableGeminiError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "429") || strings.Contains(msg, "rate") || strings.Contains(msg, "quota") {
		return true
	}
	if strings.Contains(msg, "500") || strings.Contains(msg, "502") || strings.Contains(msg, "503") || strings.Contains(msg, "504") {
		return true
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "tempor") || strings.Contains(msg, "unavailable") {
		return true
	}
	return false
}

func ptrFloat32(v float32) *float32 {
	return &v
}

func maskedKeySuffix(k string) string {
	k = strings.TrimSpace(k)
	if k == "" {
		return "-"
	}
	if len(k) <= 4 {
		return k
	}
	return k[len(k)-4:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
