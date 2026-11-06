package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"whisperserver/src/internal/domain"
	"whisperserver/src/internal/integrations/gemini"

	"github.com/labstack/echo/v4"
)

// SettingsLLMHandlers provides endpoints for viewing and updating user-specific LLM configurations.
type SettingsLLMHandlers struct {
	CurrentUserOrUnauthorized func(echo.Context) (*User, bool)
	GetSettings               func(ctx context.Context, userID string) (*domain.UserSettings, error)
	SaveSettings              func(ctx context.Context, settings domain.UserSettings) error
}

// GetHandler returns the user's current LLM preferences and the allowed model/thinking catalog.
func (h SettingsLLMHandlers) GetHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		current := gemini.DefaultPreferences()
		if h.GetSettings != nil {
			saved, err := h.GetSettings(c.Request().Context(), u.ID)
			if err == nil && saved != nil && saved.LLMPrefsJSON != "" {
				var parsed gemini.UserLLMPreferences
				if err := json.Unmarshal([]byte(saved.LLMPrefsJSON), &parsed); err == nil {
					if gemini.ValidateUserPreferences(parsed) == nil {
						current = parsed
					}
				}
			}
		}

		thinkingLevels := make([]string, 0, len(gemini.SupportedThinkingLevels))
		for tl := range gemini.SupportedThinkingLevels {
			thinkingLevels = append(thinkingLevels, string(tl))
		}

		return c.JSON(http.StatusOK, gemini.CatalogResponse{
			Models:         gemini.SupportedModels,
			ThinkingLevels: thinkingLevels,
			Current:        current,
		})
	}
}

// PutHandler validates and saves the user's updated LLM preferences.
func (h SettingsLLMHandlers) PutHandler() echo.HandlerFunc {
	return func(c echo.Context) error {
		if h.CurrentUserOrUnauthorized == nil {
			return c.NoContent(http.StatusServiceUnavailable)
		}
		u, ok := h.CurrentUserOrUnauthorized(c)
		if !ok || u == nil {
			return nil
		}

		var req gemini.UserLLMPreferences
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "invalid request body",
			})
		}

		if err := gemini.ValidateUserPreferences(req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": err.Error(),
			})
		}

		b, err := json.Marshal(req)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "failed to serialize preferences",
			})
		}

		if h.SaveSettings != nil {
			if err := h.SaveSettings(c.Request().Context(), domain.UserSettings{
				UserID:       u.ID,
				LLMPrefsJSON: string(b),
			}); err != nil {
				return c.JSON(http.StatusInternalServerError, map[string]string{
					"error": "failed to save settings: " + err.Error(),
				})
			}
		}

		return c.JSON(http.StatusOK, map[string]any{
			"status":  "ok",
			"current": req,
		})
	}
}

