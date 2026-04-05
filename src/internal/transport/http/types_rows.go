package httptransport

// Keep field order and types aligned with the legacy httpx types to allow
// cheap type conversions in wiring code during migration.

type JobRow struct {
	ID              string
	Filename        string
	FileType        string
	MediaDuration   string
	SizeBytes       int64
	Status          string
	Phase           string
	ProgressPercent int
	StatusDetail    string
	IsRefined       bool
	TagText         string
	FolderID        string
	ClientUploadID  string
	IsTrashed       bool
	UpdatedAt       string
	DeletedAt       string
	OwnerName       string
	FolderName      string
}

type FolderRow struct {
	ID        string
	Name      string
	ParentID  string
	UpdatedAt string
}
