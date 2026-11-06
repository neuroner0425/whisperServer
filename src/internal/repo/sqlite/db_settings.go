package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"whisperserver/src/internal/domain"
)

// GetUserSettings fetches the user-specific settings by user ID.
func GetUserSettings(ctx context.Context, userID string) (*domain.UserSettings, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("no database connection available")
	}

	query := `
		SELECT user_id, llm_prefs_json, updated_at
		FROM user_settings
		WHERE user_id = ?
	`
	row := dbConn.QueryRowContext(ctx, query, userID)

	var (
		s          domain.UserSettings
		updatedRaw string
	)
	err := row.Scan(&s.UserID, &s.LLMPrefsJSON, &updatedRaw)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetUserSettings failed: %w", err)
	}
	s.UpdatedAt, _ = parseSQLiteTime(updatedRaw)
	return &s, nil
}

// SaveUserSettings creates or updates user settings.
func SaveUserSettings(ctx context.Context, settings domain.UserSettings) error {
	if dbConn == nil {
		return fmt.Errorf("no database connection available")
	}

	query := `
		INSERT INTO user_settings (user_id, llm_prefs_json, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			llm_prefs_json = excluded.llm_prefs_json,
			updated_at = excluded.updated_at
	`
	now := settings.UpdatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}

	_, err := dbConn.ExecContext(ctx, query, settings.UserID, settings.LLMPrefsJSON, formatSQLiteTime(now))
	if err != nil {
		return fmt.Errorf("SaveUserSettings failed: %w", err)
	}
	return nil
}

