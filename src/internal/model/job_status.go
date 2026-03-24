package model

const (
	JobStatusPendingCode         = 10
	JobStatusRunningCode         = 20
	JobStatusRefiningPendingCode = 30
	JobStatusRefiningCode        = 40
	JobStatusCompletedCode       = 50
	JobStatusFailedCode          = 60
)

func JobStatusName(code int) string {
	switch code {
	case JobStatusPendingCode:
		return "작업 대기 중"
	case JobStatusRunningCode:
		return "작업 중"
	case JobStatusRefiningPendingCode:
		return "정제 대기 중"
	case JobStatusRefiningCode:
		return "정제 중"
	case JobStatusCompletedCode:
		return "완료"
	case JobStatusFailedCode:
		return "실패"
	default:
		return ""
	}
}

func JobStatusCode(name string) int {
	switch name {
	case "작업 대기 중":
		return JobStatusPendingCode
	case "작업 중":
		return JobStatusRunningCode
	case "정제 대기 중":
		return JobStatusRefiningPendingCode
	case "정제 중":
		return JobStatusRefiningCode
	case "완료":
		return JobStatusCompletedCode
	case "실패":
		return JobStatusFailedCode
	default:
		return 0
	}
}
