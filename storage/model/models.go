package model

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
	Id        uint64    `json:"id"`
	Topic     string    `json:"topic"`
	Records   []Record  `json:"recording_files"`
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

// returns absolute path to:
// recFolder - the folder with the recording, named after the recording id,
// dateFolder - the folder with all recordings for the day
func (r Record) Paths(repositoryRoot string) (recFolder string, dateFolder string) {
	return fmt.Sprintf("%s/%s/%s", repositoryRoot, r.DateTime[:10], r.Id), fmt.Sprintf("%s/%s", repositoryRoot, r.DateTime[:10])
}

// CloudRecordingReport describes the cloud recording report
type CloudRecordingReport struct {
	From                  string                  `json:"from"`
	To                    string                  `json:"to"`
	CloudRecordingStorage []CloudRecordingStorage `json:"cloud_recording_storage"`
}

// CloudRecordingStorage describes the cloud recording storage
type CloudRecordingStorage struct {
	Date         string   `json:"date"`
	FreeUsage    FileSize `json:"free_usage"`              // ex: "free_usage":"1.2 TB"
	PlanUsage    FileSize `json:"plan_usage"`              // ex: "plan_usage":"0"
	Usage        FileSize `json:"usage"`                   // ex: "usage":"94.72 GB"
	UsagePercent int      `json:"usage_percent,omitempty"` // ex: "usage_rate":"19"
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

// MarshalJSON implements the json.Marshaler interface for FileSize
func (f FileSize) MarshalJSON() ([]byte, error) {
	return fmt.Appendf(nil, `"%s"`, f.String()), nil
}

// UnmarshalJSON implements the json.Unmarshaler interface for FileSize
func (f *FileSize) UnmarshalJSON(data []byte) error {
	var usage string
	if err := json.Unmarshal(data, &usage); err != nil {
		return err
	}

	bytes, err := parseUsageToBytes(usage)
	if err != nil {
		return err
	}

	*f = FileSize(bytes)
	return nil
}

func parseUsageToBytes(usage string) (int64, error) {
	parts := strings.Fields(usage)
	if len(parts) < 1 {
		return 0, fmt.Errorf("invalid format: %s", usage)
	}

	value, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return 0, err
	}

	if len(parts) < 2 {
		return int64(value), nil
	}

	unit := strings.ToUpper(parts[1])
	switch unit {
	case "B", "BYTES":
		return int64(value), nil
	case "KB":
		return int64(value * 1024), nil
	case "MB":
		return int64(value * 1024 * 1024), nil
	case "GB":
		return int64(value * 1024 * 1024 * 1024), nil
	case "TB":
		return int64(value * 1024 * 1024 * 1024 * 1024), nil
	default:
		return 0, fmt.Errorf("unknown unit: %s", unit)
	}
}
