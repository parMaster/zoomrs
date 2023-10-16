package model

import (
	"fmt"
	"time"
)

// RecordStatus describes the recording status
type RecordStatus string

const (
	StatusQueued      RecordStatus = "queued"
	StatusDownloading RecordStatus = "downloading"
	StatusDownloaded  RecordStatus = "downloaded"
	StatusFailed      RecordStatus = "failed"
	StatusDeleted     RecordStatus = "deleted"
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
	From          string   `json:"from"`
	To            string   `json:"to"`
	PageSize      int      `json:"page_size"`
	PageCount     int      `json:"page_count"`
	TotalRecords  int      `json:"total_records"`
	NextPageToken string   `json:"next_page_token"`
	Meetings      Meetings `json:"meetings"`
}

// Meeting contains the meeting details
type Meeting struct {
	UUID      string    `json:"uuid"` // primary key
	Id        uint64    `json:"id"`
	Topic     string    `json:"topic"`
	Records   Records   `json:"recording_files"`
	StartTime time.Time `json:"start_time"`
	DateTime  string    `json:"date_time"`
	Duration  int       `json:"duration"`
	AccessKey string    `json:"access_key"`
}

// Record describes the records in recording_file array field
type Record struct {
	Id            string       `json:"id"`         // primary key for Record
	MeetingId     string       `json:"meeting_id"` // foreign key to Meeting.UUID
	Type          RecordType   `json:"recording_type"`
	StartTime     time.Time    `json:"recording_start"` // DateTime in RFC3339
	DateTime      string       `json:"date_time"`
	FileExtension string       `json:"file_extension"` // M4A, MP4
	FileSize      FileSize     `json:"file_size"`      // bytes
	DownloadURL   string       `json:"download_url"`
	PlayURL       string       `json:"play_url"`
	Status        RecordStatus `json:"-"`
	FilePath      string       `json:"file_path"` // local file path
}

// RecordInfo describes the records for API response
type RecordInfo struct {
	Id        string       `json:"id"`         // primary key for Record
	MeetingId string       `json:"meeting_id"` // foreign key to Meeting.UUID
	Type      RecordType   `json:"recording_type"`
	DateTime  string       `json:"date_time"`
	FileSize  FileSize     `json:"file_size"` // bytes
	Status    RecordStatus `json:"status"`
	FilePath  string       `json:"file_path"` // local file path
}

// CloudRecordingReport describes the cloud recording report
type CloudRecordingReport struct {
	From                  string                  `json:"from"`
	To                    string                  `json:"to"`
	CloudRecordingStorage []CloudRecordingStorage `json:"cloud_recording_storage"`
}

// CloudRecordingStorage describes the cloud recording storage
type CloudRecordingStorage struct {
	Date         string `json:"date"`
	FreeUsage    string `json:"free_usage"`              // ex: "free_usage":"495 GB"
	PlanUsage    string `json:"plan_usage"`              // ex: "plan_usage":"0"
	Usage        string `json:"usage"`                   // ex: "usage":"94.72 GB"
	UsagePercent int    `json:"usage_percent,omitempty"` // ex: "usage_rate":"19"
}

// FileSize describes the file size
type FileSize int64

// String returns the string representation of the file size
// in human readable format
func (f FileSize) String() string {
	const unit = 1024
	if f < unit {
		return fmt.Sprintf("%dB", f)
	}
	div, exp := int64(unit), 0
	for n := f / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB",
		float64(f)/float64(div), "kMGTPE"[exp])
}

func (f FileSize) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`"%s"`, f.String())), nil
}

// Meetings is a slice of Meeting, implements sort.Interface
// Can be used like sort.Sort(meetings) or sort.Sort(sort.Reverse(meetings))
type Meetings []Meeting

func (m Meetings) Len() int {
	return len(m)
}

func (m Meetings) Less(i, j int) bool {
	return m[i].StartTime.Before(m[j].StartTime)
}

func (m Meetings) Swap(i, j int) {
	m[i], m[j] = m[j], m[i]
}

// Records is a slice of Record, implements sort.Interface
type Records []Record

func (r Records) Len() int {
	return len(r)
}

func (r Records) Less(i, j int) bool {
	return r[i].StartTime.Before(r[j].StartTime)
}

func (r Records) Swap(i, j int) {
	r[i], r[j] = r[j], r[i]
}
