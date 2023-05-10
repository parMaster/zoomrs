package storage

import (
	"errors"

	"github.com/parMaster/zoomrs/storage/model"
)

var (
	ErrNoRows = errors.New("no rows in result set")
)

type Storer interface {
	SaveMeeting(meeting model.Meeting) error
	GetMeeting(UUID string) (*model.Meeting, error)
	ListMeetings() ([]model.Meeting, error)
	GetRecords(UUID string) ([]model.Record, error)
	DeleteMeeting(UUID string) error
	UpdateRecord(Id string, status model.RecordStatus) error
}
