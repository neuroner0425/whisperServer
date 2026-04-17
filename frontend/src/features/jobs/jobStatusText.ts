type JobStatusLike = {
  StatusCode?: number
  Status?: string
  Phase?: string
  ProgressPercent?: number
  StatusDetail?: string
}

const STATUS_PENDING = 10
const STATUS_RUNNING = 20
const STATUS_REFINING_PENDING = 30
const STATUS_REFINING = 40
const STATUS_COMPLETED = 50
const STATUS_FAILED = 60
const STATUS_AUDIO_CONVERT_FAILED = 61
const STATUS_PDF_CONVERT_FAILED = 62
const STATUS_TRANSCRIBE_FAILED = 63
const STATUS_REFINE_FAILED = 64
const STATUS_PDF_EXTRACT_FAILED = 65

export function buildJobStatusText(job: JobStatusLike) {
  const statusCode = job.StatusCode ?? 0
  const status = (job.Status || '').trim()
  const phase = (job.Phase || '').trim()
  const detail = (job.StatusDetail || '').trim()
  const progress = Math.max(0, job.ProgressPercent ?? 0)

  if (
    statusCode === STATUS_FAILED ||
    statusCode === STATUS_AUDIO_CONVERT_FAILED ||
    statusCode === STATUS_PDF_CONVERT_FAILED ||
    statusCode === STATUS_TRANSCRIBE_FAILED ||
    statusCode === STATUS_REFINE_FAILED ||
    statusCode === STATUS_PDF_EXTRACT_FAILED
  ) {
    return detail ? `${status || phase} (${detail})` : status || phase || '-'
  }
  if (statusCode === STATUS_COMPLETED) {
    return status || '완료'
  }
  if (phase === '업로드 처리 중') {
    return `업로드 처리 중 ${progress}%`
  }
  if (phase === '업로드 처리 실패') {
    return detail ? `업로드 처리 실패 (${detail})` : '업로드 처리 실패'
  }
  if (statusCode === STATUS_RUNNING || status === '작업 중') {
    return `${phase || '전사 중'} ${progress}%`
  }
  if (statusCode === STATUS_REFINING || status === '정제 중') {
    return `${phase || '정제 중'} ${progress}%`
  }
  if (statusCode === STATUS_PENDING || statusCode === STATUS_REFINING_PENDING) {
    return status || phase || '-'
  }
  if (phase) {
    return phase
  }
  return status || '-'
}
