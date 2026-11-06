// metrics.go forwards to the unified metrics registry in obs.
package server

import (
	"whisperserver/src/internal/obs"
)

var (
	jobsTotal      = obs.JobsTotal
	jobsInProgress = obs.JobsInProgress
	jobDurationSec = obs.JobDurationSec
	uploadBytes    = obs.UploadBytes
	queueLength    = obs.QueueLength
)
