// db_search_fts.go manages full-text search indexing with SQLite FTS5.
package sqlite

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"whisperserver/src/internal/structured"
)

// ensureSearchFTS ensures the SQLite FTS5 virtual table exists with unicode61 tokenizer.
func ensureSearchFTS(db *sql.DB) error {
	stmt := `CREATE VIRTUAL TABLE IF NOT EXISTS job_search_fts USING fts5(
		job_id UNINDEXED,
		owner_id UNINDEXED,
		content,
		tokenize='unicode61'
	);`
	_, err := db.Exec(stmt)
	return err
}

// IndexJobSearchContent updates or inserts the searchable content for a job.
func IndexJobSearchContent(jobID, ownerID, content string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	content = strings.TrimSpace(content)
	tx, err := dbConn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`DELETE FROM job_search_fts WHERE job_id = ?`, jobID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO job_search_fts(job_id, owner_id, content) VALUES (?, ?, ?)`, jobID, ownerID, content); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteJobSearchIndex removes full-text search entries for the given job IDs.
func DeleteJobSearchIndex(jobIDs []string) error {
	if dbConn == nil || len(jobIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(jobIDs))
	args := make([]any, len(jobIDs))
	for i, id := range jobIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`DELETE FROM job_search_fts WHERE job_id IN (%s)`, strings.Join(placeholders, ","))
	_, err := dbConn.Exec(query, args...)
	return err
}

// sanitizeFTS5Query transforms arbitrary user input into a safe and robust FTS5 match query.
// Punctuation and non-word characters are split as separate tokens so terms like "Node.js" become "Node"* "js"*.
func sanitizeFTS5Query(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var normalized strings.Builder
	for _, r := range raw {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			normalized.WriteRune(r)
		} else {
			normalized.WriteRune(' ')
		}
	}

	tokens := strings.Fields(normalized.String())
	if len(tokens) == 0 {
		return ""
	}

	safeTokens := make([]string, 0, len(tokens))
	for _, s := range tokens {
		safeTokens = append(safeTokens, fmt.Sprintf(`"%s"*`, s))
	}
	return strings.Join(safeTokens, " ")
}

// SearchJobIDsByContent returns matching job IDs for an owner using FTS5 full text search.
func SearchJobIDsByContent(ownerID, rawQuery string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	matchExpr := sanitizeFTS5Query(rawQuery)
	if matchExpr == "" {
		return nil, nil
	}

	var rows *sql.Rows
	var err error
	if ownerID == "" {
		rows, err = dbConn.Query(`SELECT job_id FROM job_search_fts WHERE job_search_fts MATCH ?`, matchExpr)
	} else {
		rows, err = dbConn.Query(`SELECT job_id FROM job_search_fts WHERE owner_id = ? AND job_search_fts MATCH ?`, ownerID, matchExpr)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		jobIDs = append(jobIDs, id)
	}
	return jobIDs, rows.Err()
}

// ExtractSearchableText extracts plain text suitable for full-text search from various JSON kinds.
func ExtractSearchableText(kind, data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}

	switch kind {
	case BlobKindTranscriptJSON:
		type transcriptPayload struct {
			Segments []struct {
				Text string `json:"text"`
			} `json:"segments"`
		}
		var parsed transcriptPayload
		if err := json.Unmarshal([]byte(data), &parsed); err == nil && len(parsed.Segments) > 0 {
			parts := make([]string, 0, len(parsed.Segments))
			for _, seg := range parsed.Segments {
				t := strings.TrimSpace(seg.Text)
				if t != "" {
					parts = append(parts, t)
				}
			}
			return strings.Join(parts, " ")
		}

	case BlobKindRefined:
		type refinedPayload struct {
			Paragraph []struct {
				ParagraphSummary string `json:"paragraph_summary"`
				Sentence         []struct {
					Content string `json:"content"`
				} `json:"sentence"`
			} `json:"paragraph"`
		}
		var parsed refinedPayload
		if err := json.Unmarshal([]byte(data), &parsed); err == nil && len(parsed.Paragraph) > 0 {
			parts := make([]string, 0, len(parsed.Paragraph)*4)
			for _, p := range parsed.Paragraph {
				if s := strings.TrimSpace(p.ParagraphSummary); s != "" {
					parts = append(parts, s)
				}
				for _, sent := range p.Sentence {
					if c := strings.TrimSpace(sent.Content); c != "" {
						parts = append(parts, c)
					}
				}
			}
			return strings.Join(parts, " ")
		}

	case BlobKindDocumentJSON:
		md, err := structured.RenderDocumentMarkdown([]byte(data))
		if err == nil && strings.TrimSpace(md) != "" {
			return md
		}
	}

	return ""
}

// SyncExistingJobsToFTS inspects all existing jobs and indexes any missing full-text content.
func SyncExistingJobsToFTS(db *sql.DB) error {
	rows, err := db.Query(`
		SELECT j.id, j.owner_id
		FROM jobs j
		WHERE j.id NOT IN (SELECT job_id FROM job_search_fts)
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	type targetJob struct {
		id      string
		ownerID string
	}
	var targets []targetJob
	for rows.Next() {
		var t targetJob
		if err := rows.Scan(&t.id, &t.ownerID); err != nil {
			return err
		}
		targets = append(targets, t)
	}
	_ = rows.Close()

	for _, t := range targets {
		// Prefer refined, then document_json, then transcript_json
		var data, kind string
		err := db.QueryRow(`
			SELECT data, kind FROM job_json 
			WHERE job_id = ? AND kind IN ('refined', 'document_json', 'transcript_json')
			ORDER BY CASE kind 
				WHEN 'refined' THEN 1 
				WHEN 'document_json' THEN 2 
				WHEN 'transcript_json' THEN 3 
				ELSE 4 END 
			LIMIT 1
		`, t.id).Scan(&data, &kind)
		var text string
		if err == nil {
			decompressed, decErr := decompressGzipIfCompressed([]byte(data))
			if decErr != nil {
				decompressed = []byte(data)
			}
			text = ExtractSearchableText(kind, string(decompressed))
		}
		_, _ = db.Exec(`INSERT INTO job_search_fts(job_id, owner_id, content) VALUES (?, ?, ?)`, t.id, t.ownerID, text)
	}
	return nil
}
