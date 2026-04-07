package sqlite

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	model "whisperserver/src/internal/domain"
)

// CreateUser inserts a new user row with a generated internal ID.
func CreateUser(loginID, email, passwordHash string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	id := uuid.NewString()
	_, err := dbConn.Exec(`INSERT INTO users(id, login_id, email, password_hash) VALUES (?, ?, ?, ?)`, id, loginID, email, passwordHash)
	return err
}

// FindUserByIdentifier loads a user by email or login ID.
func FindUserByIdentifier(identifier string) (*model.UserRecord, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	var u model.UserRecord
	err := dbConn.QueryRow(`SELECT id, login_id, email, password_hash FROM users WHERE lower(email) = lower(?) OR login_id = ?`, identifier, identifier).
		Scan(&u.ID, &u.LoginID, &u.Email, &u.PasswordHash)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// UpsertTag inserts or updates a tag definition for one owner.
func UpsertTag(ownerID, name, description string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		INSERT INTO tags(id, owner_id, name, description, updated_at) VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(owner_id, name) DO UPDATE SET description=excluded.description, updated_at=CURRENT_TIMESTAMP
	`, uuid.NewString(), ownerID, name, description)
	return err
}

// ListTagsByOwner returns every tag owned by the user.
func ListTagsByOwner(ownerID string) ([]model.Tag, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`SELECT name, description FROM tags WHERE owner_id = ? ORDER BY name`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Tag{}
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.Name, &t.Description); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTagNamesByOwner returns tag names as a set for validation.
func ListTagNamesByOwner(ownerID string) (map[string]struct{}, error) {
	tags, err := ListTagsByOwner(ownerID)
	if err != nil {
		return nil, err
	}
	out := map[string]struct{}{}
	for _, t := range tags {
		out[t.Name] = struct{}{}
	}
	return out, nil
}

// GetTagDescriptionsByNames returns descriptions for the requested tag names.
func GetTagDescriptionsByNames(ownerID string, names []string) (map[string]string, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	out := map[string]string{}
	if len(names) == 0 {
		return out, nil
	}

	stmt := `SELECT name, description FROM tags WHERE owner_id = ? AND name = ?`
	for _, n := range names {
		var name, desc string
		if err := dbConn.QueryRow(stmt, ownerID, n).Scan(&name, &desc); err != nil {
			continue
		}
		out[name] = desc
	}
	return out, nil
}

// DeleteTag removes one tag definition for the owner.
func DeleteTag(ownerID, name string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`DELETE FROM tags WHERE owner_id = ? AND name = ?`, ownerID, name)
	return err
}
