package domain

import "time"

// StepStatus defines the execution status of a pipeline step attempt.
type StepStatus string

const (
	StepStatusRunning   StepStatus = "running"
	StepStatusCompleted StepStatus = "completed"
	StepStatusFailed    StepStatus = "failed"
)

// JobStep records an immutable execution attempt of a workflow step for provenance and auditability.
type JobStep struct {
	JobID         string     `json:"job_id"`
	Step          StepType   `json:"step"`
	Attempt       int        `json:"attempt"`
	Status        StepStatus `json:"status"`
	InputHash     string     `json:"input_hash,omitempty"`
	ArtifactKey   string     `json:"artifact_key,omitempty"`
	Engine        string     `json:"engine,omitempty"`
	Model         string     `json:"model,omitempty"`
	PromptSHA     string     `json:"prompt_sha,omitempty"`
	ParamsJSON    string     `json:"params_json,omitempty"`
	QualityReport string     `json:"quality_report,omitempty"`
	ErrorMessage  string     `json:"error_message,omitempty"`
	StartedAt     time.Time  `json:"started_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

// StepResult is returned by a StepHandler upon successful or failed completion.
type StepResult struct {
	NextStep      *StepType `json:"next_step,omitempty"`
	NextPayload   string    `json:"next_payload,omitempty"`
	NextClass     *ResourceClass `json:"next_class,omitempty"`
	ArtifactKey   string    `json:"artifact_key,omitempty"`
	QualityReport string    `json:"quality_report,omitempty"`
	ParamsJSON    string    `json:"params_json,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

