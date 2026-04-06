package domain

// Folder is the persisted folder tree node owned by a user.
type Folder struct {
	ID        string
	OwnerID   string
	Name      string
	ParentID  string
	IsTrashed bool
	UpdatedAt string
}
