type JobStatusLike = {
  Status?: string
  Phase?: string
  ProgressPercent?: number
  StatusDetail?: string
}

export function buildJobStatusText(job: JobStatusLike) {
  const status = (job.Status || '').trim()
  const phase = (job.Phase || '').trim()
  const detail = (job.StatusDetail || '').trim()
  const progress = Math.max(0, job.ProgressPercent ?? 0)

  if (status === '실패') {
    return detail ? `${phase || status} (${detail})` : phase || status
  }
  if (phase === '업로드 처리 중') {
    return `업로드 처리 중 ${progress}%`
  }
  if (phase === '업로드 처리 실패') {
    return detail ? `업로드 처리 실패 (${detail})` : '업로드 처리 실패'
  }
  if (status === '작업 중') {
    return `${phase || '전사 중'} ${progress}%`
  }
  if (status === '정제 중') {
    return `${phase || '정제 중'} ${progress}%`
  }
  if (phase) {
    return phase
  }
  return status || '-'
}
