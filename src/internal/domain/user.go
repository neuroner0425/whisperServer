package domain

// UserRecord is the persisted authentication record loaded from SQLite.
type UserRecord struct {
	ID           string
	LoginID      string
	Email        string
	PasswordHash string
}
