package obs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"sync"
)

var (
	defaultLoggerOnce sync.Once
	rootLogger        *slog.Logger
)

// InitLogger configures the default structured JSON logger.
func InitLogger(w io.Writer, level slog.Level) *slog.Logger {
	if w == nil {
		w = os.Stdout
	}
	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	})
	rootLogger = slog.New(handler)
	slog.SetDefault(rootLogger)
	return rootLogger
}

// GetLogger returns the configured root slog.Logger or falls back to standard JSON logger.
func GetLogger() *slog.Logger {
	defaultLoggerOnce.Do(func() {
		if rootLogger == nil {
			InitLogger(os.Stdout, slog.LevelInfo)
		}
	})
	return rootLogger
}

// JobLogger creates a contextualized child logger with standard job execution attributes:
// job_id, owner_id, step, and attempt.
func JobLogger(jobID, ownerID, step string, attempt int) *slog.Logger {
	attrs := []any{
		slog.String("job_id", jobID),
	}
	if ownerID != "" {
		attrs = append(attrs, slog.String("owner_id", ownerID))
	}
	if step != "" {
		attrs = append(attrs, slog.String("step", step))
	}
	if attempt > 0 {
		attrs = append(attrs, slog.Int("attempt", attempt))
	}
	return GetLogger().With(attrs...)
}

// WithJobContext attaches standard job execution attributes to a context.Context.
type jobContextKey struct{}

type JobContext struct {
	JobID   string
	OwnerID string
	Step    string
	Attempt int
}

func ContextWithJob(ctx context.Context, jc JobContext) context.Context {
	return context.WithValue(ctx, jobContextKey{}, jc)
}

// FromContext extracts the job logger from context, or returns the default logger if none is set.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx != nil {
		if v := ctx.Value(jobContextKey{}); v != nil {
			if jc, ok := v.(JobContext); ok {
				return JobLogger(jc.JobID, jc.OwnerID, jc.Step, jc.Attempt)
			}
		}
	}
	return GetLogger()
}

