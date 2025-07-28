package storage

import (
	"context"
	"errors"

	"github.com/parMaster/zoomrs/storage/model"
)

var (
	ErrNoRows = errors.New("no rows in result set")
)

//go:generate moq -out storer_moq_test.go . Storer

type Storer interface {
	SaveMeeting(ctx context.Context, meeting model.Meeting) error
	GetMeeting(ctx context.Context, UUID string) (*model.Meeting, error)
	ListMeetings(ctx context.Context) ([]model.Meeting, error)
	GetMeetings(ctx context.Context) ([]model.Meeting, error)
	GetRecords(ctx context.Context, UUID string) ([]model.Record, error)
	GetRecordsByStatus(ctx context.Context, rs model.RecordStatus) ([]model.Record, error)
	DeleteMeeting(ctx context.Context, UUID string) error
	UpdateRecord(ctx context.Context, Id string, status model.RecordStatus, path string) error
	GetQueuedRecord(ctx context.Context) (*model.Record, error)
	ResetFailedRecords(ctx context.Context) error
	Stats(ctx context.Context) (map[model.RecordStatus]any, error)
}
