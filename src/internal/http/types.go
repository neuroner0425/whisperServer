package httpx

type JobView struct {
	Filename        string
	Status          string
	UploadedAt      string
	StartedAt       string
	CompletedAt     string
	Duration        string
	MediaDuration   string
	Phase           string
	ProgressLabel   string
	ProgressPercent int
	PreviewText     string
}

type JobRow struct {
	ID            string
	Filename      string
	MediaDuration string
	Status        string
	IsRefined     bool
	TagText       string
	FolderID      string
	IsTrashed     bool
	UpdatedAt     string
	DeletedAt     string
	OwnerName     string
	FolderName    string
}

type FolderRow struct {
	ID        string
	Name      string
	ParentID  string
	UpdatedAt string
}
