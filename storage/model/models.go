package model

import (
	"time"
)

// RecordStatus describes the recording status
type RecordStatus string

const (
	Queued      RecordStatus = "queued"
	Downloading RecordStatus = "downloading"
	Downloaded  RecordStatus = "downloaded"
	Failed      RecordStatus = "failed"
)

// RecordType describes the cloud recording types
type RecordType string

func (r RecordType) String() string {
	return string(r)
}

const (
	AudioOnly                   RecordType = "audio_only"
	ChatFile                    RecordType = "chat_file"
	SharedScreenWithSpeakerView RecordType = "shared_screen_with_speaker_view"
	SharedScreenWithGalleryView RecordType = "shared_screen_with_gallery_view"
)

// Recordings - json response from zoom api
type Recordings struct {
	From          string    `json:"from"`
	To            string    `json:"to"`
	PageSize      int       `json:"page_size"`
	PageCount     int       `json:"page_count"`
	TotalRecords  int       `json:"total_records"`
	NextPageToken string    `json:"next_page_token"`
	Meetings      []Meeting `json:"meetings"`
}

// Meeting contains the meeting details
type Meeting struct {
	UUID      string    `json:"uuid"` // primary key
	Topic     string    `json:"topic"`
	Records   []Record  `json:"recording_files"`
	StartTime time.Time `json:"start_time"`
}

// Record describes the records in recording_file array field
type Record struct {
	Id            string       `json:"id"`         // primary key for Record
	MeetingId     string       `json:"meeting_id"` // foreign key to Meeting.UUID
	Type          RecordType   `json:"recording_type"`
	StartTime     time.Time    `json:"recording_start"` // DateTime in RFC3339
	FileExtension string       `json:"file_extension"`  // M4A, MP4
	DownloadURL   string       `json:"download_url"`
	PlayURL       string       `json:"play_url"`
	Status        RecordStatus `json:"-"`
	FilePath      string       `json:"-"` // local file path
}
