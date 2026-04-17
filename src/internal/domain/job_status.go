package domain

// Canonical job status codes persisted in SQLite and reused across the app.
const (
	JobStatusPendingCode            = 10
	JobStatusRunningCode            = 20
	JobStatusRefiningPendingCode    = 30
	JobStatusRefiningCode           = 40
	JobStatusCompletedCode          = 50
	JobStatusFailedCode             = 60
	JobStatusAudioConvertFailedCode = 61
	JobStatusPDFConvertFailedCode   = 62
	JobStatusTranscribeFailedCode   = 63
	JobStatusRefineFailedCode       = 64
	JobStatusPDFExtractFailedCode   = 65
)

// JobStatusName converts a numeric code into the localized status label.
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
	case JobStatusAudioConvertFailedCode:
		return "오디오 변환 실패"
	case JobStatusPDFConvertFailedCode:
		return "PDF 변환 실패"
	case JobStatusTranscribeFailedCode:
		return "전사 실패"
	case JobStatusRefineFailedCode:
		return "정제 실패"
	case JobStatusPDFExtractFailedCode:
		return "PDF 추출 실패"
	default:
		return ""
	}
}

// JobStatusCode converts a localized status label into its numeric code.
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
	case "오디오 변환 실패":
		return JobStatusAudioConvertFailedCode
	case "PDF 변환 실패":
		return JobStatusPDFConvertFailedCode
	case "전사 실패":
		return JobStatusTranscribeFailedCode
	case "정제 실패":
		return JobStatusRefineFailedCode
	case "PDF 추출 실패":
		return JobStatusPDFExtractFailedCode
	default:
		return 0
	}
}
