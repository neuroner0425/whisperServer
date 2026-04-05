package store

import "fmt"

type JobBlobUsage struct {
	JobID     string
	Bytes     int64
	BlobCount int
}

func SaveJobBlob(jobID, kind string, data []byte) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO job_blobs(job_id, kind, data, updated_at) VALUES (?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(job_id, kind) DO UPDATE SET data=excluded.data, updated_at=CURRENT_TIMESTAMP
	`, jobID, kind, data)
	return err
}

func LoadJobBlob(jobID, kind string) ([]byte, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	var b []byte
	err := dbConn.QueryRow(`SELECT data FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&b)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func HasJobBlob(jobID, kind string) bool {
	if dbConn == nil {
		return false
	}
	var n int
	err := dbConn.QueryRow(`SELECT COUNT(1) FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

func DeleteJobBlobs(jobID string) {
	if dbConn == nil {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ?`, jobID)
}

func DeleteJobBlob(jobID, kind string) {
	if dbConn == nil {
		return
	}
	_, _ = dbConn.Exec(`DELETE FROM job_blobs WHERE job_id = ? AND kind = ?`, jobID, kind)
}

func ListJobBlobKinds(jobID string) ([]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT kind FROM job_blobs WHERE job_id = ? ORDER BY kind`, jobID)
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

func ListJobBlobUsageByOwner(ownerID string) ([]JobBlobUsage, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`
		SELECT j.id, COALESCE(SUM(LENGTH(b.data)), 0) AS size_bytes, COUNT(b.kind) AS blob_count
		FROM jobs j
		LEFT JOIN job_blobs b ON b.job_id = j.id AND b.kind <> ?
		WHERE j.owner_id = ?
		GROUP BY j.id
		HAVING COALESCE(SUM(LENGTH(b.data)), 0) > 0
		ORDER BY size_bytes DESC, j.filename COLLATE NOCASE ASC
	`, BlobKindWav, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []JobBlobUsage{}
	for rows.Next() {
		var item JobBlobUsage
		if err := rows.Scan(&item.JobID, &item.Bytes, &item.BlobCount); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func JobBlobUsageMapByOwner(ownerID string) (map[string]int64, error) {
	items, err := ListJobBlobUsageByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(items))
	for _, item := range items {
		out[item.JobID] = item.Bytes
	}
	return out, nil
}
