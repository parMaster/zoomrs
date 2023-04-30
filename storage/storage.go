package storage

import (
	"github.com/parMaster/zoomrs/storage/model"
)

type Storer interface {
	SaveMeeting(meeting *model.Meeting) error
	SaveRecord(UUID string, record *model.Record) error
	GetMeeting(UUID string) (*model.Meeting, error)
	ListMeetings() ([]model.Meeting, error)
	DeleteMeeting(UUID string) error
	UpdateRecord(Id string, status string) error
}
