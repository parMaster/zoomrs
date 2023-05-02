package main

/*
import (
	"time"

	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

// Repository will implement such features as:
// - saving meetings to the storage, including record files of specific types
// - syncing meetings with the storage
// - updating recordings status - queued, downloading, downloaded, failed
// - listing meetings and records
// - deleting meetings from the storage
// files management won't be implemented here

// Repository interface
type Repository interface {
	// SaveMeeting saves meeting to the storage
	SaveMeeting(meeting Meeting) error
	// SyncMeeting syncs meeting with the storage
	SyncMeetings(meetings *[]Meeting) error
	// UpdateMeeting updates meeting in the storage
	UpdateRecording(Id string, status RecordingStatus) error
	// ListMeetings lists meetings from the storage
	ListMeetings() (map[string]Meeting, error)
	// DeleteMeeting deletes meeting from the storage
	DeleteMeeting(UUID string) error
	// SyncableRecordTypes sets syncable record types
	SyncableRecordTypes(types []RecordType)
}

// Repo is the storage repository
type Repo struct {
	store    storage.Storer
	syncable map[RecordType]bool
}

// NewRepository creates new repository
func NewRepository(store storage.Storer) Repository {
	r := &Repo{store: store}
	r.syncable = make(map[RecordType]bool)
	return r
}

// TODO: add syncable record types to the config
// SyncableRecordTypes sets syncable record types
func (r *Repo) SyncableRecordTypes(types []RecordType) {
	for _, t := range types {
		r.syncable[t] = true
	}
}

// SaveMeeting saves meeting to the storage
func (r *Repo) SaveMeeting(meeting Meeting) error {
	var records []model.Record
	for _, record := range meeting.RecordingFiles {
		if r.syncable[RecordType(record.RecordingType)] {
			records = append(records, model.Record{Id: record.Id, Type: record.RecordingType.String(), Status: string(Queued), Url: record.DownloadURL, Path: ""})
		}
	}

	err := r.store.SaveMeeting(model.Meeting{UUID: meeting.UUID, Topic: meeting.Topic, DateTime: meeting.StartTime.Format("2006-01-02 15:04:05"), Records: records})
	return err
}

// SyncMeeting syncs meeting with the storage
func (r *Repo) SyncMeetings(meetings *[]Meeting) error {

	repoMeetings, err := r.ListMeetings()
	if err != nil {
		return err
	}

	for _, meeting := range *meetings {
		if _, ok := repoMeetings[meeting.UUID]; !ok {
			err := r.SaveMeeting(meeting)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

// UpdateMeeting updates meeting in the storage
func (r *Repo) UpdateRecording(Id string, status RecordingStatus) error {
	err := r.store.UpdateRecord(Id, string(status))
	return err
}

// ListMeetings lists meetings from the storage
func (r *Repo) ListMeetings() (map[string]Meeting, error) {
	meetings := make(map[string]Meeting)

	storeMeetings, err := r.store.ListMeetings()
	if err != nil {
		return nil, err
	}

	for _, sm := range storeMeetings {

		records, err := r.store.GetRecords(sm.UUID)
		if err != nil {
			return nil, err
		}
		RecordingFiles := make([]RecordingFile, len(records))
		for _, record := range records {
			RecordingFiles = append(RecordingFiles, RecordingFile{Id: record.Id, RecordingType: RecordingType(record.Type), DownloadURL: record.Url, Status: RecordingStatus(record.Status)})
		}

		meetingDateTime, err := time.Parse("2006-01-02 15:04:05", sm.DateTime)
		if err != nil {
			return nil, err
		}
		meetings[sm.UUID] = Meeting{UUID: sm.UUID, Topic: sm.Topic, StartTime: meetingDateTime, RecordingFiles: RecordingFiles}

	}

	return meetings, nil
}

// DeleteMeeting deletes meeting from the storage
func (r *Repo) DeleteMeeting(UUID string) error {
	err := r.store.DeleteMeeting(UUID)
	return err
}

*/
