package domain

import "time"

// Event types published to outbox and streaming clients.
const (
	EventTypeJobStatus      = "job.status"
	EventTypeJobProgress    = "job.progress"
	EventTypeFolderChanged  = "folder.changed"
	EventTypeFilesChanged   = "files.changed"
	EventTypeIndexRequested = "index.requested"
)

// DomainEvent represents a persisted outbox event waiting for reliable external delivery.
type DomainEvent struct {
	Seq         int64      `json:"seq"`
	OwnerID     string     `json:"owner_id"`
	JobID       string     `json:"job_id,omitempty"`
	EventType   string     `json:"event_type"`
	PayloadJSON string     `json:"payload_json"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
}

