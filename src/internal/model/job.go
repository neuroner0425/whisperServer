package model

type Job struct {
	Status               string   `json:"status,omitempty"`
	Filename             string   `json:"filename,omitempty"`
	Result               string   `json:"result,omitempty"`
	UploadedAt           string   `json:"uploaded_at,omitempty"`
	UploadedTS           float64  `json:"uploaded_ts,omitempty"`
	Duration             string   `json:"duration,omitempty"`
	MediaDuration        string   `json:"media_duration,omitempty"`
	MediaDurationSeconds *int     `json:"media_duration_seconds,omitempty"`
	Description          string   `json:"description,omitempty"`
	RefineEnabled        bool     `json:"refine_enabled,omitempty"`
	OwnerID              string   `json:"owner_id,omitempty"`
	Tags                 []string `json:"tags,omitempty"`
	FolderID             string   `json:"folder_id,omitempty"`
	IsTrashed            bool     `json:"is_trashed,omitempty"`
	StartedAt            string   `json:"started_at,omitempty"`
	StartedTS            float64  `json:"started_ts,omitempty"`
	CompletedAt          string   `json:"completed_at,omitempty"`
	CompletedTS          float64  `json:"completed_ts,omitempty"`
	Phase                string   `json:"phase,omitempty"`
	ProgressPercent      int      `json:"progress_percent,omitempty"`
	ProgressLabel        string   `json:"progress_label,omitempty"`
	PreviewText          string   `json:"preview_text,omitempty"`
	ResultRefined        string   `json:"result_refined,omitempty"`
	StatusDetail         string   `json:"status_detail,omitempty"`
}

func (j *Job) Clone() *Job {
	if j == nil {
		return nil
	}
	out := *j
	if j.Tags != nil {
		out.Tags = append([]string(nil), j.Tags...)
	}
	if j.MediaDurationSeconds != nil {
		v := *j.MediaDurationSeconds
		out.MediaDurationSeconds = &v
	}
	return &out
}

func (j *Job) IsRefined() bool {
	return j != nil && j.ResultRefined != "" && j.Status == "완료"
}
