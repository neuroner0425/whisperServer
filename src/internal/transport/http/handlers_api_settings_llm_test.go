package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/gemini"

	"github.com/labstack/echo/v4"
)

func TestSettingsLLMHandlers_GetAndPut(t *testing.T) {
	e := echo.New()

	var storedSettings *domain.UserSettings
	fakeUser := &User{ID: "test_user_1", Email: "test@example.com"}

	handlers := SettingsLLMHandlers{
		CurrentUserOrUnauthorized: func(c echo.Context) (*User, bool) {
			return fakeUser, true
		},
		GetSettings: func(ctx context.Context, userID string) (*domain.UserSettings, error) {
			return storedSettings, nil
		},
		SaveSettings: func(ctx context.Context, s domain.UserSettings) error {
			storedSettings = &s
			return nil
		},
	}

	// 1. GET initial (returns defaults)
	req := httptest.NewRequest(http.MethodGet, "/api/settings/llm", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	if err := handlers.GetHandler()(c); err != nil {
		t.Fatalf("GET handler failed: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", rec.Code)
	}

	var cat gemini.CatalogResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &cat); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if cat.Current.Polish.Model != gemini.ModelGemini25FlashLite {
		t.Fatalf("expected default polish model flash-lite, got %s", cat.Current.Polish.Model)
	}

	// 2. PUT invalid model -> 400 Bad Request
	badBody := []byte(`{"polish":{"model":"unsupported-model","thinking_level":"low"}}`)
	reqBad := httptest.NewRequest(http.MethodPut, "/api/settings/llm", bytes.NewReader(badBody))
	reqBad.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recBad := httptest.NewRecorder()
	cBad := e.NewContext(reqBad, recBad)

	if err := handlers.PutHandler()(cBad); err != nil {
		t.Fatalf("PUT handler failed: %v", err)
	}
	if recBad.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", recBad.Code)
	}

	// 3. PUT valid preferences -> 200 OK
	validPrefs := gemini.DefaultPreferences()
	validPrefs.Polish.Model = gemini.ModelGemini25Flash
	validPrefs.Polish.ThinkingLevel = gemini.ThinkingMedium

	validJSON, _ := json.Marshal(validPrefs)
	reqGood := httptest.NewRequest(http.MethodPut, "/api/settings/llm", bytes.NewReader(validJSON))
	reqGood.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recGood := httptest.NewRecorder()
	cGood := e.NewContext(reqGood, recGood)

	if err := handlers.PutHandler()(cGood); err != nil {
		t.Fatalf("PUT good handler failed: %v", err)
	}
	if recGood.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d, body: %s", recGood.Code, recGood.Body.String())
	}

	// 4. GET again -> reflects updated preferences
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/settings/llm", nil), rec2)
	if err := handlers.GetHandler()(c2); err != nil {
		t.Fatalf("GET 2 handler failed: %v", err)
	}
	var cat2 gemini.CatalogResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &cat2)
	if cat2.Current.Polish.Model != gemini.ModelGemini25Flash || cat2.Current.Polish.ThinkingLevel != gemini.ThinkingMedium {
		t.Fatalf("expected updated polish preferences, got %+v", cat2.Current.Polish)
	}
}

