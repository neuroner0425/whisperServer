package queue

import (
	"context"
	"errors"
)

// TaskKind identifies the type of work the worker should perform.
type TaskKind string

const (
	TaskAudioTranscribe TaskKind = "audio_transcribe"
	TaskAudioRefine     TaskKind = "audio_refine"
	TaskPDFExtract      TaskKind = "pdf_extract"
)

// Task is the minimal unit stored in the queue.
type Task struct {
	JobID string
	Kind  TaskKind
}

// ErrClosed reports queue operations attempted after shutdown.
var ErrClosed = errors.New("queue closed")

// Queue abstracts enqueue/dequeue operations away from the worker implementation.
type Queue interface {
	Enqueue(Task) error
	Dequeue(context.Context) (Task, error)
	Len() int
	Close()
}
