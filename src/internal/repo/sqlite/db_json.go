package sqlite

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

func compressGzip(data string) ([]byte, error) {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write([]byte(data)); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressGzipIfCompressed(b []byte) ([]byte, error) {
	if len(b) >= 2 && b[0] == 0x1f && b[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	}
	return b, nil
}

// SaveJobJSON upserts one named JSON payload for a job, compressing with gzip.
// It also indexes searchable text into FTS5 for search-relevant kinds.
func SaveJobJSON(jobID, kind, data string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}

	compressed, err := compressGzip(data)
	if err != nil {
		// Fallback to storing raw if compression fails
		compressed = []byte(data)
	}

	_, err = dbConn.Exec(`
		INSERT INTO job_json(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, compressed)
	if err != nil {
		return err
	}

	// Update full text search index if this kind contains user-facing content
	if kind == BlobKindRefined || kind == BlobKindTranscriptJSON || kind == BlobKindDocumentJSON {
		var ownerID string
		_ = dbConn.QueryRow(`SELECT owner_id FROM jobs WHERE id = ?`, jobID).Scan(&ownerID)
		text := ExtractSearchableText(kind, data)
		if text != "" {
			_ = IndexJobSearchContent(jobID, ownerID, text)
		}
	}

	return nil
}

// LoadJobJSON returns one named JSON payload for a job, decompressing gzip if needed.
func LoadJobJSON(jobID, kind string) (string, error) {
	if dbConn == nil {
		return "", fmt.Errorf("db is not initialized")
	}
	var raw []byte
	err := dbConn.QueryRow(`SELECT data FROM job_json WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&raw)
	if err != nil {
		return "", err
	}
	decompressed, err := decompressGzipIfCompressed(raw)
	if err != nil {
		return string(raw), nil
	}
	return string(decompressed), nil
}

// HasJobJSON reports whether a named JSON payload exists for a job.
func HasJobJSON(jobID, kind string) bool {
	if dbConn == nil {
		return false
	}
	var n int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_json WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

// DeleteJobJSON removes one named JSON payload attached to a job.
func DeleteJobJSON(jobID, kind string) {
	if dbConn == nil {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_json WHERE job_id = ? AND kind = ?`, jobID, kind)
}

// ListJobJSONKinds lists every stored JSON kind for a job.
func ListJobJSONKinds(jobID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT kind FROM job_json WHERE job_id = ? ORDER BY kind`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []string{}
	for rows.Next() {
		var kind string
		if err := rows.Scan(&kind); err != nil {
			return nil, err
		}
		out = append(out, kind)
	}
	return out, rows.Err()
}
