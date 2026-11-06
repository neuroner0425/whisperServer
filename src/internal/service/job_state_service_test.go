package service

import (
	"context"
	"testing"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/repo/sqlite"
)

type mockBroker struct {
	notifiedUser string
	notifiedType string
	lastPayload  map[string]any
}

func (m *mockBroker) Notify(userID, eventType string, payload map[string]any) {
	m.notifiedUser = userID
	m.notifiedType = eventType
	m.lastPayload = payload
}

func setupStateTestDB(t *testing.T) {
	t.Helper()
	projectRoot := t.TempDir()
	if err := sqlite.Init(projectRoot); err != nil {
		t.Fatalf("sqlite.Init failed: %v", err)
	}
	t.Cleanup(func() {
		sqlite.Close()
	})
}

func TestJobStateService_Transition_AtomicOutbox(t *testing.T) {
	setupStateTestDB(t)
	ctx := context.Background()

	// Insert test user and job
	rawDB := sqlite.GetRawDBForTest()
	_, _ = rawDB.Exec(`INSERT INTO users (id, email, password_hash) VALUES ('user_state', 'u@test.com', 'h')`)
	_, _ = rawDB.Exec(`INSERT INTO jobs (id, owner_id, status_code, filename) VALUES ('job_state_1', 'user_state', 10, 'audio.mp3')`)

	txMgr := sqlite.NewTxManager(nil)
	broker := &mockBroker{}
	stateSvc := NewJobStateService(txMgr, broker)

	// 1. Valid transition
	err := stateSvc.Transition(ctx, StateTransitionParams{
		JobID:        "job_state_1",
		OwnerID:      "user_state",
		StatusCode:   20,
		StatusName:   "작업 중",
		StatusDetail: "전사 처리 중...",
	})
	if err != nil {
		t.Fatalf("Transition failed: %v", err)
	}

	// Verify DB state updated
	var code int
	var ver int
	err = rawDB.QueryRow(`SELECT status_code, version FROM jobs WHERE id = 'job_state_1'`).Scan(&code, &ver)
	if err != nil || code != 20 || ver != 2 {
		t.Fatalf("expected status_code 20 version 2, got code=%d ver=%d err=%v", code, ver, err)
	}

	// Verify Outbox event created
	unpub, err := sqlite.FetchUnpublishedOutbox(ctx, 10)
	if err != nil || len(unpub) != 1 {
		t.Fatalf("expected 1 outbox event, got %d (err: %v)", len(unpub), err)
	}
	if unpub[0].EventType != domain.EventTypeJobStatus || unpub[0].JobID != "job_state_1" {
		t.Fatalf("unexpected outbox event: %+v", unpub[0])
	}

	// 2. Unauthorized transition (wrong owner) -> should fail
	err = stateSvc.Transition(ctx, StateTransitionParams{
		JobID:      "job_state_1",
		OwnerID:    "wrong_owner",
		StatusCode: 50,
	})
	if err == nil {
		t.Fatalf("expected unauthorized error for wrong owner")
	}
}

func TestJobStateService_ReportProgress_Volatile(t *testing.T) {
	broker := &mockBroker{}
	stateSvc := NewJobStateService(nil, broker)

	snapshot := domain.ProgressSnapshot{
		StatusCode:      20,
		Phase:           "전사 중",
		ProgressPercent: 55,
		PreviewText:     "실시간 프리뷰 텍스트입니다.",
	}

	stateSvc.ReportProgress("user_progress", "job_p1", snapshot)

	if broker.notifiedUser != "user_progress" || broker.notifiedType != domain.EventTypeJobProgress {
		t.Fatalf("unexpected broker call: user=%s type=%s", broker.notifiedUser, broker.notifiedType)
	}
	if broker.lastPayload["progress_percent"] != 55 || broker.lastPayload["preview_text"] != snapshot.PreviewText {
		t.Fatalf("unexpected payload: %+v", broker.lastPayload)
	}
}

