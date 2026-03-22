export type JobView = {
  Filename: string
  FileType?: string
  Status: string
  UploadedAt: string
  StartedAt: string
  CompletedAt: string
  Duration: string
  MediaDuration: string
  Phase: string
  ProgressLabel: string
  ProgressPercent: number
  PreviewText: string
}

export type AvailableTag = {
  Name: string
  Description: string
}

export type JobDetailResponse = {
  job_id: string
  current_user_name: string
  job: JobView
  tag_text: string
  selected_tags: string[]
  status: string
  view: 'waiting' | 'preview' | 'result'
  preview_text?: string
  original_text?: string
  text?: string
  has_refined?: boolean
  can_refine?: boolean
  variant?: 'original' | 'refined'
  audio_url?: string
  download_text_url?: string
  download_refined_url?: string
  available_tags?: AvailableTag[]
}
