package bolt

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/parMaster/zoomrs/storage/model"
	bolt "go.etcd.io/bbolt"
)

type BoltStorage struct {
	DB  *bolt.DB
	ctx context.Context
}

func NewBoltDB(ctx context.Context, path string) (*BoltStorage, error) {

	db, err := bolt.Open(path, 0777, nil)
	if err != nil {
		return nil, fmt.Errorf("open db: %s", err)
	}

	go func() {
		<-ctx.Done()
		db.Close()
	}()

	// Start a writable transaction.
	tx, err := db.Begin(true)
	if err != nil {
		return nil, fmt.Errorf("begin tx: %s", err)
	}
	defer tx.Rollback()

	_, err = tx.CreateBucketIfNotExists([]byte("meetings"))
	if err != nil {
		return nil, fmt.Errorf("create meetings bucket: %s", err)
	}

	_, err = tx.CreateBucketIfNotExists([]byte("records"))
	if err != nil {
		return nil, fmt.Errorf("create records bucket: %s", err)
	}

	// Commit the transaction and check for error
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit tx: %s", err)
	}

	return &BoltStorage{DB: db, ctx: ctx}, nil
}

// SaveMeeting(meeting model.Meeting) error
func (s *BoltStorage) SaveMeeting(meeting model.Meeting) error {

	// Start a writable transaction.
	tx, err := s.DB.Begin(true)
	if err != nil {
		return fmt.Errorf("SaveMeeting begin tx: %s", err)
	}
	defer tx.Rollback()

	b := tx.Bucket([]byte("meetings"))

	meetingStr, err := json.Marshal(meeting)
	if err != nil {
		return fmt.Errorf("error marshaling meeting: %s", err)
	}

	err = b.Put([]byte(meeting.UUID), meetingStr)
	if err != nil {
		return fmt.Errorf("error put meeting: %s", err)
	}

	// for _, r := range meeting.Records {
	// 	err := s.saveRecord(r)
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	// Commit the transaction and check for error
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("SaveMeeting commit tx: %s", err)
	}

	return nil
}

// GetMeeting(UUID string) (*model.Meeting, error)
func (s *BoltStorage) GetMeeting(UUID string) (*model.Meeting, error) {

	var meetingStr []byte
	s.DB.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("meetings"))

		meetingStr = b.Get([]byte(UUID))
		if meetingStr == nil {
			return fmt.Errorf("GetMeeting no meeting found")
		}

		return nil
	})

	var meeting model.Meeting
	err := json.Unmarshal(meetingStr, &meeting)
	if err != nil {
		return nil, fmt.Errorf("GetMeeting unmarshal meeting: %s", err)
	}

	return &meeting, nil
}

// 	ListMeetings() ([]model.Meeting, error)
// 	GetMeetings() ([]model.Meeting, error)
// 	GetRecords(UUID string) ([]model.Record, error)
// 	GetRecordsByStatus(model.RecordStatus) ([]model.Record, error)
// 	GetRecordsInfo(UUID string) ([]model.RecordInfo, error)
// 	DeleteMeeting(UUID string) error
// 	UpdateRecord(Id string, status model.RecordStatus, path string) error
// 	GetQueuedRecord() (*model.Record, error)
// 	ResetFailedRecords() error
// 	Stats() (map[model.RecordStatus]interface{}, error)
// }
