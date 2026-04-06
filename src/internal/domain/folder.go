package domain

type Folder struct {
	ID        string
	OwnerID   string
	Name      string
	ParentID  string
	IsTrashed bool
	UpdatedAt string
}
