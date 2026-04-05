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
  StatusDetail?: string
  PageCount?: number
  ProcessedPageCount?: number
  CurrentChunk?: number
  TotalChunks?: number
  ResumeAvailable?: boolean
}

export type AvailableTag = {
  Name: string
  Description: string
}

export type DocumentPageElement = {
  header?: {
    level: number
    text: string
  }
  text?: string
  math_inline?: string
  math_block?: string
  list?: string[]
  img?: {
    title: string
    description: string
  }
  table?: {
    title: string
    rows: Array<{
      cells: string[]
    }>
  }
}

export type DocumentPage = {
  page_index: number
  elements: DocumentPageElement[]
}

export type DocumentResponse = {
  pages: DocumentPage[]
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
  download_document_json_url?: string
  original_pdf_url?: string
  page_count?: number
  processed_page_count?: number
  current_chunk?: number
  total_chunks?: number
  resume_available?: boolean
  available_tags?: AvailableTag[]
}
