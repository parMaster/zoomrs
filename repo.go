package main

import "github.com/parMaster/zoomrs/storage"

// Repository will implement such features as:
// - saving meetings to the storage, including record files of specific types
// - syncing meetings with the storage
// - updating recordings status - queued, downloading, downloaded, failed
// - listing meetings and records
// - deleting meetings from the storage
// files management won't be implemented here

// RecordingStatus describes the recording status
type RecordingStatus string

const (
	// Queued status
	Queued RecordingStatus = "queued"
	// Downloading status
	Downloading RecordingStatus = "downloading"
	// Downloaded status
	Downloaded RecordingStatus = "downloaded"
	// Failed status
	Failed RecordingStatus = "failed"
)

// Repository interface
type Repository interface {
	// SaveMeeting saves meeting to the storage
	SaveMeeting(meeting *Meeting) error
	// SyncMeeting syncs meeting with the storage
	SyncMeetings(meetings *[]Meeting) error
	// UpdateMeeting updates meeting in the storage
	UpdateRecording(Id string, status RecordingStatus) error
	// ListMeetings lists meetings from the storage
	ListMeetings() ([]Meeting, error)
	// DeleteMeeting deletes meeting from the storage
	DeleteMeeting(UUID string) error
}

// Repo is the storage repository
type Repo struct {
	store *storage.Storer
}

// NewRepository creates new repository
func NewRepository(store storage.Storer) Repository {
	return &Repo{store: &store}
}

// SaveMeeting saves meeting to the storage
func (r *Repo) SaveMeeting(meeting *Meeting) error {
	return nil
}

// SyncMeeting syncs meeting with the storage
func (r *Repo) SyncMeetings(meetings *[]Meeting) error {
	return nil
}

// UpdateMeeting updates meeting in the storage
func (r *Repo) UpdateRecording(Id string, status RecordingStatus) error {
	return nil
}

// ListMeetings lists meetings from the storage
func (r *Repo) ListMeetings() ([]Meeting, error) {
	return nil, nil
}

// DeleteMeeting deletes meeting from the storage
func (r *Repo) DeleteMeeting(UUID string) error {
	return nil
}
