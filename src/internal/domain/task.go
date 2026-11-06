package domain

import "time"

// ResourceClass classifies worker capability required to process a task.
type ResourceClass string

const (
	ResourceClassMedia ResourceClass = "media"
	ResourceClassSTT   ResourceClass = "stt"
	ResourceClassLLM   ResourceClass = "llm"
	ResourceClassIndex ResourceClass = "index"
)

// StepType identifies a discrete pipeline step.
type StepType string

const (
	StepAVScan     StepType = "av_scan"
	StepNormalize  StepType = "normalize"
	StepTranscribe StepType = "transcribe"
	StepPolish     StepType = "polish"
	StepStructure  StepType = "structure"
	StepPDFRender  StepType = "pdf_render"
	StepPDFChunk   StepType = "pdf_chunk"
	StepPDFMerge   StepType = "pdf_merge"
	StepIndex      StepType = "index"
)

// TaskStatus represents the lifecycle state of a task in the durable queue.
type TaskStatus string

const (
	TaskStatusReady  TaskStatus = "ready"
	TaskStatusLeased TaskStatus = "leased"
	TaskStatusDone   TaskStatus = "done"
	TaskStatusDead   TaskStatus = "dead"
)

// Task represents a durable unit of work claimed and executed by workers.
type Task struct {
	ID            string        `json:"id"`
	JobID         string        `json:"job_id"`
	OwnerID       string        `json:"owner_id"`
	Step          StepType      `json:"step"`
	ResourceClass ResourceClass `json:"resource_class"`
	Priority      int           `json:"priority"`
	PayloadJSON   string        `json:"payload_json"`
	Status        TaskStatus    `json:"status"`
	LeaseOwner    *string       `json:"lease_owner,omitempty"`
	LeaseUntil    *time.Time    `json:"lease_until,omitempty"`
	Attempt       int           `json:"attempt"`
	MaxAttempts   int           `json:"max_attempts"`
	RunAfter      time.Time     `json:"run_after"`
	CreatedAt     time.Time     `json:"created_at"`
}

