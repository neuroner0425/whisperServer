package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

// columnExists reports whether a table already contains the named column.
func columnExists(db *sql.DB, tableName, columnName string) (bool, error) {
	rows, err := db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

// ensureStatusCodes seeds the canonical status code table.
func ensureStatusCodes(db *sql.DB) error {
	statements := []string{
		`INSERT INTO status_codes(code, name) VALUES (10, '작업 대기 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (20, '작업 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (30, '정제 대기 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (40, '정제 중') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (50, '완료') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (60, '실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (61, '오디오 변환 실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (62, 'PDF 변환 실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (63, '전사 실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (64, '정제 실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
		`INSERT INTO status_codes(code, name) VALUES (65, 'PDF 추출 실패') ON CONFLICT(code) DO UPDATE SET name=excluded.name`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func tableHasColumn(queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}, tableName, columnName string) (bool, error) {
	rows, err := queryer.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, tableName))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid        int
			name       string
			columnType string
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultVal, &pk); err != nil {
			return false, err
		}
		if name == columnName {
			return true, nil
		}
	}
	return false, rows.Err()
}

// encodeTagsJSON serializes job tags for SQLite storage.
func encodeTagsJSON(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, err := json.Marshal(tags)
	if err != nil {
		errorf("db.encodeTagsJSON", err, "count=%d", len(tags))
		return "[]"
	}
	return string(b)
}

// decodeTagsJSON deserializes job tags from SQLite storage.
func decodeTagsJSON(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var tags []string
	if err := json.Unmarshal([]byte(value), &tags); err != nil {
		errorf("db.decodeTagsJSON", err, "value=%s", value)
		return nil
	}
	return tags
}

// intOrNil unwraps an optional int for SQL parameters.
func intOrNil(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

// boolToInt converts booleans into SQLite-friendly integers.
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
