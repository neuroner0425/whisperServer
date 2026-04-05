package httpx

type JobView struct {
	Filename           string
	FileType           string
	Status             string
	UploadedAt         string
	StartedAt          string
	CompletedAt        string
	Duration           string
	MediaDuration      string
	Phase              string
	ProgressLabel      string
	ProgressPercent    int
	PreviewText        string
	StatusDetail       string
	PageCount          int
	ProcessedPageCount int
	CurrentChunk       int
	TotalChunks        int
	ResumeAvailable    bool
}

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
