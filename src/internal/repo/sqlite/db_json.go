package sqlite

import "fmt"

// SaveJobJSON upserts one named JSON payload for a job.
func SaveJobJSON(jobID, kind, data string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO job_json(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, data)
	return err
}

// LoadJobJSON returns one named JSON payload for a job.
func LoadJobJSON(jobID, kind string) (string, error) {
	if dbConn == nil {
		return "", fmt.Errorf("db is not initialized")
	}
	var data string
	err := dbConn.QueryRow(`SELECT data FROM job_json WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&data)
	if err != nil {
		return "", err
	}
	return data, nil
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
