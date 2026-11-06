package domain

import "time"

// UserSettings represents persistent user-specific preferences (e.g. LLM presets).
type UserSettings struct {
	UserID       string    `json:"user_id"`
	LLMPrefsJSON string    `json:"llm_prefs_json"`
	UpdatedAt    time.Time `json:"updated_at"`
}

