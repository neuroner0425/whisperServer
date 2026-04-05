package queue

import (
	"context"
	"errors"
)

type TaskKind string

const (
	TaskAudioTranscribe TaskKind = "audio_transcribe"
	TaskAudioRefine     TaskKind = "audio_refine"
	TaskPDFExtract      TaskKind = "pdf_extract"
)

type Task struct {
	JobID string
	Kind  TaskKind
}

var ErrClosed = errors.New("queue closed")

type Queue interface {
	Enqueue(Task) error
	Dequeue(context.Context) (Task, error)
	Len() int
	Close()
}

