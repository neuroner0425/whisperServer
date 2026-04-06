// status.go keeps the user-facing status labels used across runtime, worker, and transport.
package server

const (
	statusPending         = "작업 대기 중"
	statusRunning         = "작업 중"
	statusRefiningPending = "정제 대기 중"
	statusRefining        = "정제 중"
	statusCompleted       = "완료"
	statusFailed          = "실패"
)
