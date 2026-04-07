package sqlite

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	model "whisperserver/src/internal/domain"
)

// DeleteTrashedFoldersByOwner permanently removes trashed folders for one user.
func DeleteTrashedFoldersByOwner(ownerID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`DELETE FROM folders WHERE owner_id = ? AND is_trashed = 1`, ownerID)
	return err
}

// CreateFolder inserts a new folder row and returns its generated ID.
func CreateFolder(ownerID, name, parentID string) (string, error) {
	if dbConn == nil {
		return "", fmt.Errorf("db is not initialized")
	}
	id := uuid.NewString()
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	_, err := dbConn.Exec(`
		INSERT INTO folders(id, owner_id, name, parent_id, is_trashed, updated_at)
		VALUES (?, ?, ?, ?, 0, CURRENT_TIMESTAMP)
	`, id, ownerID, name, emptyStringAsNil(parentID))
	return id, err
}

// ListFoldersByParent lists direct child folders under the requested parent.
func ListFoldersByParent(ownerID, parentID string, trashed bool) ([]model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	rows, err := dbConn.Query(`
		SELECT id, owner_id, name, COALESCE(parent_id, ''), is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND COALESCE(parent_id, '') = ? AND is_trashed = ?
		ORDER BY name
	`, ownerID, parentID, boolToInt(trashed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Folder{}
	for rows.Next() {
		var f model.Folder
		var trashedInt int
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.IsTrashed = trashedInt != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListAllFoldersByOwner lists every folder owned by a user with a shared trash filter.
func ListAllFoldersByOwner(ownerID string, trashed bool) ([]model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	rows, err := dbConn.Query(`
		SELECT id, owner_id, name, COALESCE(parent_id, ''), is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND is_trashed = ?
		ORDER BY name
	`, ownerID, boolToInt(trashed))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []model.Folder{}
	for rows.Next() {
		var f model.Folder
		var trashedInt int
		if err := rows.Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.IsTrashed = trashedInt != 0
		out = append(out, f)
	}
	return out, rows.Err()
}

// GetFolderByID loads one folder row for the given owner.
func GetFolderByID(ownerID, folderID string) (*model.Folder, error) {
	if dbConn == nil {
		return nil, fmt.Errorf("db is not initialized")
	}
	var f model.Folder
	var trashedInt int
	err := dbConn.QueryRow(`
		SELECT id, owner_id, name, COALESCE(parent_id, ''), is_trashed, updated_at
		FROM folders
		WHERE owner_id = ? AND id = ?
	`, ownerID, folderID).Scan(&f.ID, &f.OwnerID, &f.Name, &f.ParentID, &trashedInt, &f.UpdatedAt)
	if err != nil {
		return nil, err
	}
	f.IsTrashed = trashedInt != 0
	return &f, nil
}

// SetFolderTrashed toggles trash state for a folder and all of its descendants.
func SetFolderTrashed(ownerID, folderID string, trashed bool) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		WITH RECURSIVE folder_tree(id) AS (
			SELECT id FROM folders WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id FROM folders f
			JOIN folder_tree ft ON f.parent_id = ft.id
			WHERE f.owner_id = ?
		)
		UPDATE folders
		SET is_trashed = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id IN (SELECT id FROM folder_tree)
	`, ownerID, folderID, ownerID, boolToInt(trashed))
	return err
}

// ListFolderPath walks parent links to build a breadcrumb path.
func ListFolderPath(ownerID, folderID string) ([]model.Folder, error) {
	if strings.TrimSpace(folderID) == "" {
		return nil, nil
	}
	path := []model.Folder{}
	cur := folderID
	for strings.TrimSpace(cur) != "" {
		f, err := GetFolderByID(ownerID, cur)
		if err != nil {
			break
		}
		path = append([]model.Folder{*f}, path...)
		cur = f.ParentID
	}
	return path, nil
}

// RenameFolder updates a folder name and refreshes its timestamp.
func RenameFolder(ownerID, folderID, newName string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	_, err := dbConn.Exec(`
		UPDATE folders
		SET name = ?, updated_at = CURRENT_TIMESTAMP
		WHERE owner_id = ? AND id = ?
	`, newName, ownerID, folderID)
	return err
}

// MoveFolder changes the parent folder for an existing folder row.
func MoveFolder(ownerID, folderID, parentID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	if strings.TrimSpace(parentID) == "" {
		parentID = ""
	}
	_, err := dbConn.Exec(`
		UPDATE folders
		SET parent_id = ?, updated_at = CURRENT_TIMESTAMP
		WHERE owner_id = ? AND id = ?
	`, emptyStringAsNil(parentID), ownerID, folderID)
	return err
}

// TouchFolderAndAncestors refreshes timestamps up the folder ancestry chain.
func TouchFolderAndAncestors(ownerID, folderID string) error {
	if dbConn == nil {
		return fmt.Errorf("db is not initialized")
	}
	folderID = strings.TrimSpace(folderID)
	if folderID == "" {
		return nil
	}
	_, err := dbConn.Exec(`
		WITH RECURSIVE folder_line(id, parent_id) AS (
			SELECT id, parent_id
			FROM folders
			WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id, f.parent_id
			FROM folders f
			JOIN folder_line fl ON f.id = fl.parent_id
			WHERE f.owner_id = ?
		)
		UPDATE folders
		SET updated_at = CURRENT_TIMESTAMP
		WHERE id IN (SELECT id FROM folder_line)
	`, ownerID, folderID, ownerID)
	return err
}

// IsFolderDescendant reports whether one folder exists inside another folder subtree.
func IsFolderDescendant(ownerID, folderID, maybeDescendantID string) (bool, error) {
	if dbConn == nil {
		return false, fmt.Errorf("db is not initialized")
	}
	if folderID == "" || maybeDescendantID == "" {
		return false, nil
	}
	var n int
	err := dbConn.QueryRow(`
		WITH RECURSIVE folder_tree(id) AS (
			SELECT id FROM folders WHERE owner_id = ? AND id = ?
			UNION ALL
			SELECT f.id FROM folders f
			JOIN folder_tree ft ON f.parent_id = ft.id
			WHERE f.owner_id = ?
		)
		SELECT COUNT(1) FROM folder_tree WHERE id = ?
	`, ownerID, folderID, ownerID, maybeDescendantID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
