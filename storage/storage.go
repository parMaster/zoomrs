package storage

import (
	"errors"

	"github.com/parMaster/zoomrs/storage/model"
)

var (
	ErrNoRows = errors.New("no rows in result set")
)

//go:generate moq -out storer_moq_test.go . Storer

type Storer interface {
	SaveMeeting(meeting model.Meeting) error
	GetMeeting(UUID string) (*model.Meeting, error)
	ListMeetings() ([]model.Meeting, error)
	GetMeetings() ([]model.Meeting, error)
	GetRecords(UUID string) ([]model.Record, error)
	GetRecordsByStatus(model.RecordStatus) ([]model.Record, error)
	DeleteMeeting(UUID string) error
	UpdateRecord(Id string, status model.RecordStatus, path string) error
	GetQueuedRecord() (*model.Record, error)
	ResetFailedRecords() error
	Stats() (map[model.RecordStatus]interface{}, error)
}
