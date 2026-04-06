// metrics.go defines the Prometheus metrics registered during bootstrap.
package server

import "github.com/prometheus/client_golang/prometheus"

var (
	jobsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "whisper_jobs_total", Help: "Total jobs finished by status"},
		[]string{"status"},
	)
	jobsInProgress = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_jobs_in_progress", Help: "Jobs currently being processed"})
	jobDurationSec = prometheus.NewHistogram(prometheus.HistogramOpts{Name: "whisper_job_duration_seconds", Help: "Duration of jobs in seconds"})
	uploadBytes    = prometheus.NewCounter(prometheus.CounterOpts{Name: "whisper_upload_bytes_total", Help: "Total bytes uploaded"})
	queueLength    = prometheus.NewGauge(prometheus.GaugeOpts{Name: "whisper_task_queue_size", Help: "Task queue size"})
)
