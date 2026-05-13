package gemini

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNextReadyClientHonorsGlobalCooldown(t *testing.T) {
	now := time.Now()
	rt := &Runtime{
		clients: []geminiKeyClient{
			{key: "k1"},
			{key: "k2"},
		},
		globalCooldownUntil: now.Add(10 * time.Second),
	}

	idx, wait := rt.nextReadyClient(now)
	if idx != -1 {
		t.Fatalf("expected no ready client during global cooldown, got %d", idx)
	}
	if wait < 9*time.Second {
		t.Fatalf("expected wait close to 10s, got %s", wait)
	}

	idx, wait = rt.nextReadyClient(now.Add(11 * time.Second))
	if idx < 0 {
		t.Fatalf("expected a ready client after global cooldown, got wait=%s", wait)
	}
}

func TestOnFailureSetsMinimumGlobalCooldown(t *testing.T) {
	now := time.Now()
	rt := &Runtime{
		clients: []geminiKeyClient{{key: "k1"}},
	}

	rt.onFailure(0, errors.New("timeout"))
	wait := rt.globalCooldownUntil.Sub(now)
	if wait < 9*time.Second {
		t.Fatalf("expected global cooldown close to 10s, got %s", wait)
	}
}

func TestWaitForReadyClientReturnsContextErrorDuringCooldown(t *testing.T) {
	now := time.Now()
	rt := &Runtime{
		clients:             []geminiKeyClient{{key: "k1"}},
		globalCooldownUntil: now.Add(time.Minute),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	idx, err := rt.waitForReadyClient(ctx)
	if err == nil {
		t.Fatalf("expected context error, got idx=%d", idx)
	}
	if idx != -1 {
		t.Fatalf("expected no selected client, got %d", idx)
	}
}
