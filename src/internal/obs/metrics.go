package obs

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metricsOnce sync.Once

	// Core P0-06 Prometheus Metrics
	JobStepDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "whisper_job_step_duration_seconds",
			Help:    "Duration of each job processing step in seconds",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600},
		},
		[]string{"step", "status"},
	)

	QueueWaitDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "whisper_queue_wait_seconds",
			Help:    "Queue waiting time before worker processing begins",
			Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120},
		},
		[]string{"resource_class"},
	)

	GeminiRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "whisper_gemini_requests_total",
			Help: "Total Gemini API requests partitioned by model and status_code",
		},
		[]string{"model", "status_code"},
	)

	GeminiTokensTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "whisper_gemini_tokens_total",
			Help: "Total tokens consumed by Gemini requests (prompt vs candidates)",
		},
		[]string{"direction"},
	)

	SSEReloadsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "whisper_sse_reloads_total",
			Help: "Total SSE reload or event triggers",
		},
	)

	// Legacy server metrics maintained for backward compatibility
	JobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "whisper_jobs_total", Help: "Total jobs finished by status"},
		[]string{"status"},
	)
	JobsInProgress = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_jobs_in_progress", Help: "Jobs currently being processed"})
	JobDurationSec = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "whisper_job_duration_seconds", Help: "Duration of jobs in seconds"})
	UploadBytes    = prometheus.NewCounter(prometheus.CounterOpts{Name: "whisper_upload_bytes_total", Help: "Total bytes uploaded"})
	QueueLength    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_task_queue_size", Help: "Task queue size"})
)

// RegisterMetrics safely registers all collectors into the default Prometheus registry once.
func RegisterMetrics() {
	metricsOnce.Do(func() {
		prometheus.MustRegister(
			JobStepDuration,
			QueueWaitDuration,
			GeminiRequestsTotal,
			GeminiTokensTotal,
			SSEReloadsTotal,
			JobsTotal,
			JobsInProgress,
			JobDurationSec,
			UploadBytes,
			QueueLength,
		)
	})
}

// ObserveJobStep records the latency of a step (e.g. transcribe, refine, pdf_extract).
func ObserveJobStep(step, status string, d time.Duration) {
	if step == "" {
		step = "unknown"
	}
	if status == "" {
		status = "unknown"
	}
	JobStepDuration.WithLabelValues(step, status).Observe(d.Seconds())
}

// ObserveQueueWait records the queuing latency before execution starts.
func ObserveQueueWait(resourceClass string, d time.Duration) {
	if resourceClass == "" {
		resourceClass = "default"
	}
	QueueWaitDuration.WithLabelValues(resourceClass).Observe(d.Seconds())
}

// RecordGeminiRequest counts an API invocation with model and status code.
func RecordGeminiRequest(model, statusCode string) {
	if model == "" {
		model = "gemini-unknown"
	}
	if statusCode == "" {
		statusCode = "200"
	}
	GeminiRequestsTotal.WithLabelValues(model, statusCode).Inc()
}

// RecordGeminiTokens records prompt and candidates token counts.
func RecordGeminiTokens(promptTokens, candidatesTokens int) {
	if promptTokens > 0 {
		GeminiTokensTotal.WithLabelValues("prompt").Add(float64(promptTokens))
	}
	if candidatesTokens > 0 {
		GeminiTokensTotal.WithLabelValues("candidates").Add(float64(candidatesTokens))
	}
}

// IncSSEReload increments the SSE reload / change event counter.
func IncSSEReload() {
	SSEReloadsTotal.Inc()
}

