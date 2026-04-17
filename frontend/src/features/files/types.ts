export type Tag = {
  Name: string
  Description: string
}

export type FolderNode = {
  ID: string
  Name: string
  ParentID?: string
  UpdatedAt?: string
}

export type JobItem = {
  ID: string
  Filename: string
  FileType?: string
  MediaDuration: string
  SizeBytes: number
  StatusCode?: number
  Status: string
  Phase?: string
  ProgressPercent?: number
  StatusDetail?: string
  IsRefined: boolean
  TagText: string
  FolderID: string
  ClientUploadID?: string
  UpdatedAt: string
  FolderName: string
}

export type FilesResponse = {
  changed: boolean
  current_user_name: string
  view_mode: 'home' | 'explore' | 'search'
  search_query: string
  selected_tag: string
  selected_sort: 'name' | 'updated'
  selected_order: 'asc' | 'desc'
  current_folder_id: string
  folder_path: FolderNode[]
  all_folders: FolderNode[]
  tags: Tag[]
  job_items: JobItem[]
  folder_items: FolderNode[]
  page: number
  page_size: number
  total_pages: number
  total_items: number
  version: string
  upload_limits?: {
    pdf_max_pages: number
    pdf_max_pages_per_request: number
  }
}
