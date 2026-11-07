package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	model "whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/storage"
	intwhisper "whisperserver/src/internal/integrations/whisper"
	"whisperserver/src/internal/service"
	"whisperserver/src/internal/worker"
)

// TestAdversarial_HMACSignatures verifies expired, tampered, and action-swapped HMAC signatures.
func TestAdversarial_HMACSignatures(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("challenger-secret-key-12345"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	h := LocalBlobHandlers{
		Storage: store,
	}
	e := echo.New()

	testKey := "jobs/job-adv-1/quarantine_raw"
	testData := []byte("confidential-audio-stream-payload")
	_, err = store.Put(context.Background(), testKey, testData, "audio/mp4")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	// 1. Expired GET URL (generate with valid duration, then set expires query param to past timestamp)
	validGetURL, err := store.PresignGet(context.Background(), testKey, "file.mp4", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}
	uGet, _ := url.Parse(validGetURL)
	qGet := uGet.Query()
	qGet.Set("expires", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10))
	uGet.RawQuery = qGet.Encode()

	req := httptest.NewRequest(http.MethodGet, uGet.String(), nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error on expired GET url, got nil")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on expired GET, got %v", err)
	}

	// 2. Expired PUT URL
	validPutURL, err := store.PresignPut(context.Background(), testKey, "audio/mp4", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignPut failed: %v", err)
	}
	uPut, _ := url.Parse(validPutURL)
	qPut := uPut.Query()
	qPut.Set("expires", strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10))
	uPut.RawQuery = qPut.Encode()

	req = httptest.NewRequest(http.MethodPut, uPut.String(), bytes.NewReader([]byte("new-data")))
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Put()(c)
	if err == nil {
		t.Fatal("expected error on expired PUT url, got nil")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on expired PUT, got %v", err)
	}

	// 3. Valid GET URL tampered in key, filename, or sig
	u, _ := url.Parse(validGetURL)
	// Tamper sig
	q := u.Query()
	q.Set("sig", "invalid0000000000000000000000000000000000000000000000000000000000")
	u.RawQuery = q.Encode()
	req = httptest.NewRequest(http.MethodGet, u.String(), nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error on tampered sig GET url, got nil")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on tampered sig GET, got %v", err)
	}

	// Tamper key
	uTamperKey, _ := url.Parse(validGetURL)
	qTK := uTamperKey.Query()
	qTK.Set("key", "jobs/job-other-user/audio_aac")
	uTamperKey.RawQuery = qTK.Encode()
	req = httptest.NewRequest(http.MethodGet, uTamperKey.String(), nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error on tampered key GET url, got nil")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on tampered key GET, got %v", err)
	}

	// Tamper filename
	uTamperFN, _ := url.Parse(validGetURL)
	qTFN := uTamperFN.Query()
	qTFN.Set("filename", "malicious_script.sh")
	uTamperFN.RawQuery = qTFN.Encode()
	req = httptest.NewRequest(http.MethodGet, uTamperFN.String(), nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error on tampered filename GET url, got nil")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden on tampered filename GET, got %v", err)
	}

	// 4. Action Swapping: Attempt to GET using PUT signature
	putU, _ := url.Parse(validPutURL)
	putSig := putU.Query().Get("sig")
	putExpires := putU.Query().Get("expires")

	swapGetURL := fmt.Sprintf("/internal/local-blob/get?key=%s&expires=%s&sig=%s", url.QueryEscape(testKey), putExpires, putSig)
	req = httptest.NewRequest(http.MethodGet, swapGetURL, nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected 403 Forbidden when using PUT signature on GET endpoint")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// 5. Missing signing query parameters
	req = httptest.NewRequest(http.MethodGet, "/internal/local-blob/get?key="+url.QueryEscape(testKey), nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected 400 Bad Request on missing signing parameters")
	}
	if he, ok := err.(*echo.HTTPError); !ok || he.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %v", err)
	}
}

// TestAdversarial_RangeRequests tests HTTP RFC 7233 Range headers under boundary conditions.
func TestAdversarial_RangeRequests(t *testing.T) {
	tmp := t.TempDir()
	store, err := storage.NewLocalStorage(tmp, storage.WithSecretKey("range-secret-key"))
	if err != nil {
		t.Fatalf("NewLocalStorage failed: %v", err)
	}

	h := LocalBlobHandlers{
		Storage: store,
	}
	e := echo.New()

	// Create 5000 bytes payload
	const totalSize = 5000
	payload := make([]byte, totalSize)
	for i := 0; i < totalSize; i++ {
		payload[i] = byte(i % 251) // prime modulus to have varied data
	}

	testKey := "jobs/job-range/audio_aac"
	_, err = store.Put(context.Background(), testKey, payload, "audio/mp4")
	if err != nil {
		t.Fatalf("store.Put failed: %v", err)
	}

	getURL, err := store.PresignGet(context.Background(), testKey, "test.m4a", 10*time.Minute)
	if err != nil {
		t.Fatalf("PresignGet failed: %v", err)
	}

	tests := []struct {
		name               string
		rangeHeader        string
		expectedStatus     int
		expectedBodyLength int
		expectedContent    []byte
		checkContentRange  string
	}{
		{
			name:               "Valid closed range bytes=0-1023",
			rangeHeader:        "bytes=0-1023",
			expectedStatus:     http.StatusPartialContent,
			expectedBodyLength: 1024,
			expectedContent:    payload[0:1024],
			checkContentRange:  "bytes 0-1023/5000",
		},
		{
			name:               "Valid open-ended range bytes=1024-",
			rangeHeader:        "bytes=1024-",
			expectedStatus:     http.StatusPartialContent,
			expectedBodyLength: totalSize - 1024,
			expectedContent:    payload[1024:totalSize],
			checkContentRange:  "bytes 1024-4999/5000",
		},
		{
			name:               "Valid suffix range bytes=-500",
			rangeHeader:        "bytes=-500",
			expectedStatus:     http.StatusPartialContent,
			expectedBodyLength: 500,
			expectedContent:    payload[totalSize-500 : totalSize],
			checkContentRange:  "bytes 4500-4999/5000",
		},
		{
			name:               "Out-of-range bytes=9999999-",
			rangeHeader:        "bytes=9999999-",
			expectedStatus:     http.StatusRequestedRangeNotSatisfiable,
			expectedBodyLength: 0,
			checkContentRange:  "bytes */5000",
		},
		{
			name:               "Malformed byte specifier bytes=invalid-range (Go http.ServeContent returns 416 without Content-Range)",
			rangeHeader:        "bytes=invalid-range",
			expectedStatus:     http.StatusRequestedRangeNotSatisfiable,
			expectedBodyLength: 0,
			checkContentRange:  "",
		},
		{
			name:               "Unrecognized range unit items=0-100 (Go http.ServeContent parseRange returns 416 invalid range)",
			rangeHeader:        "items=0-100",
			expectedStatus:     http.StatusRequestedRangeNotSatisfiable,
			expectedBodyLength: 0,
			checkContentRange:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, getURL, nil)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := h.Get()(c)
			if err != nil {
				t.Fatalf("Get() returned unexpected error: %v", err)
			}
			if rec.Code != tc.expectedStatus {
				t.Fatalf("expected status %d, got %d (body: %s)", tc.expectedStatus, rec.Code, rec.Body.String())
			}

			if tc.checkContentRange != "" {
				cr := rec.Header().Get("Content-Range")
				if cr != tc.checkContentRange {
					t.Fatalf("expected Content-Range %q, got %q", tc.checkContentRange, cr)
				}
			}

			if tc.expectedBodyLength > 0 {
				if rec.Body.Len() != tc.expectedBodyLength {
					t.Fatalf("expected body length %d, got %d", tc.expectedBodyLength, rec.Body.Len())
				}
				if tc.expectedContent != nil && !bytes.Equal(rec.Body.Bytes(), tc.expectedContent) {
					t.Fatalf("returned body does not match expected slice")
				}
			}
		})
	}
}

// TestAdversarial_R18OwnershipIsolation verifies ownership boundaries on media and upload endpoints.
func TestAdversarial_R18OwnershipIsolation(t *testing.T) {
	memStore := storage.NewMemoryStorage()
	_, _ = memStore.Put(nil, "jobs/job-user1/audio_aac", []byte("user1-secret-audio"), "audio/mp4")
	_, _ = memStore.Put(nil, "jobs/job-user2/audio_aac", []byte("user2-secret-audio"), "audio/mp4")

	jobs := map[string]*model.Job{
		"job-user1": {
			OwnerID:    "user-1",
			Filename:   "user1.m4a",
			FileType:   "audio",
			StatusCode: model.JobStatusCompletedCode,
		},
		"job-user2": {
			OwnerID:    "user-2",
			Filename:   "user2.m4a",
			FileType:   "audio",
			StatusCode: model.JobStatusCompletedCode,
		},
	}

	// Current user is user-1
	h := MediaURLHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return &User{ID: "user-1", LoginID: "user1"}, true
		},
		GetJob: func(jobID string) *model.Job {
			return jobs[jobID]
		},
		Storage: memStore,
	}
	e := echo.New()

	// 1. User 1 accesses User 2's media URL -> 403 Forbidden
	req := httptest.NewRequest(http.MethodGet, "/api/jobs/job-user2/media-url", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues("job-user2")

	err := h.Get()(c)
	if err == nil {
		t.Fatal("expected error on accessing another user's job media URL")
	}
	he, ok := err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusForbidden {
		t.Fatalf("expected 403 Forbidden, got %v", err)
	}

	// 2. User 1 accesses non-existent job -> 404
	req = httptest.NewRequest(http.MethodGet, "/api/jobs/job-nonexistent/media-url", nil)
	rec = httptest.NewRecorder()
	c = e.NewContext(req, rec)
	c.SetParamNames("job_id")
	c.SetParamValues("job-nonexistent")

	err = h.Get()(c)
	if err == nil {
		t.Fatal("expected error on accessing nonexistent job")
	}
	he, ok = err.(*echo.HTTPError)
	if !ok || he.Code != http.StatusNotFound {
		t.Fatalf("expected 404 Not Found, got %v", err)
	}

	// 3. CompletePresigned ownership isolation: Complete for missing quarantine object
	uploadSvc := service.NewUploadService(service.UploadServiceDeps{
		MaxUploadSizeMB: 20,
		Storage:         memStore,
	})
	_, err = uploadSvc.CompletePresigned(context.Background(), service.UploadCompleteRequest{
		OwnerID:  "user-1",
		JobID:    "job-unuploaded",
		Filename: "test.mp3",
	})
	if err == nil {
		t.Fatal("expected error completing presigned upload for unuploaded file")
	}
	svcErr, ok := err.(*service.HTTPError)
	if !ok || svcErr.Status != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %v", err)
	}
}

// TestAdversarial_MalwareQuarantineAndPromotion tests streaming antivirus quarantine and file promotion.
func TestAdversarial_MalwareQuarantineAndPromotion(t *testing.T) {
	memStore := storage.NewMemoryStorage()

	// Scenario A: Clean file is uploaded -> AVScan cleans and promotes to source_raw
	cleanKey := "jobs/job-clean/quarantine_raw"
	cleanData := []byte("clean audio data content")
	_, err := memStore.Put(context.Background(), cleanKey, cleanData, "audio/mp4")
	if err != nil {
		t.Fatalf("memStore.Put failed: %v", err)
	}

	mockCleanScanner := &mockMalwareScanner{
		verdict: model.VerdictClean,
		engine:  "ClamAV 1.0-Test",
	}

	avCleanHandler := &worker.AVScanStepHandler{
		Scanner: mockCleanScanner,
		Storage: memStore,
	}

	payloadClean, _ := json.Marshal(map[string]any{
		"storage_key": cleanKey,
		"file_type":   "audio",
		"language":    "ko",
	})
	taskClean := &model.Task{
		ID:          "task-clean-1",
		JobID:       "job-clean",
		Step:        model.StepAVScan,
		PayloadJSON: string(payloadClean),
	}

	stepRes, err := avCleanHandler.Execute(context.Background(), taskClean)
	if err != nil {
		t.Fatalf("AVScanStepHandler clean file failed: %v", err)
	}
	if stepRes.NextStep == nil || *stepRes.NextStep != model.StepNormalize {
		t.Fatalf("expected next step normalize, got %v", stepRes.NextStep)
	}

	// Verify quarantine_raw is removed and source_raw is created
	qExists, _ := memStore.Exists(context.Background(), cleanKey)
	if qExists {
		t.Fatal("quarantine_raw must be deleted after clean scan promotion")
	}
	sExists, _ := memStore.Exists(context.Background(), "jobs/job-clean/source_raw")
	if !sExists {
		t.Fatal("source_raw must exist after clean scan promotion")
	}
	promotedData, _ := memStore.Get(context.Background(), "jobs/job-clean/source_raw")
	if !bytes.Equal(promotedData, cleanData) {
		t.Fatalf("promoted source_raw data mismatch")
	}

	// Scenario B: Infected file is uploaded -> AVScan deletes quarantine_raw and does NOT create source_raw
	infectedKey := "jobs/job-infected/quarantine_raw"
	infectedData := []byte("EICAR_STANDARD_ANTIVIRUS_TEST_FILE")
	_, err = memStore.Put(context.Background(), infectedKey, infectedData, "audio/mp4")
	if err != nil {
		t.Fatalf("memStore.Put failed: %v", err)
	}

	mockInfectedScanner := &mockMalwareScanner{
		verdict:   model.VerdictInfected,
		signature: "Win.Test.EICAR_HDB-1",
		engine:    "ClamAV 1.0-Test",
	}

	avInfectedHandler := &worker.AVScanStepHandler{
		Scanner: mockInfectedScanner,
		Storage: memStore,
	}

	payloadInfected, _ := json.Marshal(map[string]any{
		"storage_key": infectedKey,
		"file_type":   "audio",
		"language":    "ko",
	})
	taskInfected := &model.Task{
		ID:          "task-infected-1",
		JobID:       "job-infected",
		Step:        model.StepAVScan,
		PayloadJSON: string(payloadInfected),
	}

	_, err = avInfectedHandler.Execute(context.Background(), taskInfected)
	if err == nil {
		t.Fatal("expected error on infected malware scan")
	}
	if !strings.Contains(err.Error(), "infected malware") {
		t.Fatalf("expected infected malware error, got %v", err)
	}

	// Verify quarantine_raw was immediately deleted
	qExists, _ = memStore.Exists(context.Background(), infectedKey)
	if qExists {
		t.Fatal("quarantine_raw must be immediately deleted upon infection detection")
	}

	// Verify source_raw was NEVER created
	sExists, _ = memStore.Exists(context.Background(), "jobs/job-infected/source_raw")
	if sExists {
		t.Fatal("source_raw must NOT exist for infected file")
	}
}

// TestAdversarial_TempWavLifecycle verifies that temp_stt.wav is cleaned up after transcription.
func TestAdversarial_TempWavLifecycle(t *testing.T) {
	memStore := storage.NewMemoryStorage()

	jobID := "job-temp-lifecycle"
	tempWavKey := fmt.Sprintf("jobs/%s/temp_stt.wav", jobID)
	wavBytes := []byte("RIFF....WAVEfmt ....data....pcm-audio-sample")
	_, err := memStore.Put(context.Background(), tempWavKey, wavBytes, "audio/wav")
	if err != nil {
		t.Fatalf("memStore.Put failed: %v", err)
	}

	mockWhisper := &mockWhisperRunner{
		runFn: func(ctx context.Context, jID string, audio []byte, dur *int, opts intwhisper.RunOptions) (intwhisper.RunResult, error) {
			return intwhisper.RunResult{
				TimelineText:   "[00:00:00.000 --> 00:00:05.000] Hello world transcribed",
				TranscriptJSON: []byte(`{"text":"Hello world transcribed"}`),
			}, nil
		},
	}

	transcribeHandler := &worker.TranscribeStepHandler{
		Storage:       memStore,
		WhisperRunner: mockWhisper,
		GetJob: func(id string) *model.Job {
			return &model.Job{
				Filename:      "audio.mp3",
				RefineEnabled: true,
			}
		},
	}

	task := &model.Task{
		ID:          "task-stt-1",
		JobID:       jobID,
		Step:        model.StepTranscribe,
		PayloadJSON: `{"language":"ko"}`,
	}

	stepRes, err := transcribeHandler.Execute(context.Background(), task)
	if err != nil {
		t.Fatalf("TranscribeStepHandler failed: %v", err)
	}
	if stepRes.NextStep == nil || *stepRes.NextStep != model.StepPolish {
		t.Fatalf("expected next step polish, got %v", stepRes.NextStep)
	}

	// Assert: temp_stt.wav MUST be completely removed from storage
	exists, err := memStore.Exists(context.Background(), tempWavKey)
	if err != nil {
		t.Fatalf("memStore.Exists failed: %v", err)
	}
	if exists {
		t.Fatalf("CRITICAL: temporary STT WAV file %s lingered in storage after transcription!", tempWavKey)
	}
}

// Mock helpers

type mockMalwareScanner struct {
	verdict   model.ScanVerdict
	signature string
	engine    string
}

func (m *mockMalwareScanner) Ping(ctx context.Context) error {
	return nil
}

func (m *mockMalwareScanner) ScanStream(ctx context.Context, r io.Reader, size int64) (model.ScanResult, error) {
	return model.ScanResult{
		Verdict:   m.verdict,
		Signature: m.signature,
		EngineVer: m.engine,
	}, nil
}

type mockWhisperRunner struct {
	runFn func(context.Context, string, []byte, *int, intwhisper.RunOptions) (intwhisper.RunResult, error)
}

func (m *mockWhisperRunner) RunFromBlobWithOptions(ctx context.Context, jobID string, audio []byte, dur *int, opts intwhisper.RunOptions) (intwhisper.RunResult, error) {
	if m.runFn != nil {
		return m.runFn(ctx, jobID, audio, dur, opts)
	}
	return intwhisper.RunResult{}, nil
}
