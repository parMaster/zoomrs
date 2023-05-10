package sqlite

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
)

func Test_SqliteStorage(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var err error
	store, err := NewStorage(ctx, "file:../../.tmp/test_storage.db?mode=rwc&_journal_mode=WAL")
	if err != nil {
		log.Printf("[ERROR] Failed to open SQLite storage: %e", err)
	}
	store.Cleanup()

	timeNow := time.Now()

	testRecords := []model.Record{
		{
			Id:            "Id1",
			MeetingId:     "testUUID",
			Type:          "testType",
			StartTime:     timeNow,
			FileExtension: "M4A",
			Status:        "testStatus",
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      "testFilePath",
		},
		{
			Id:            "Id2",
			MeetingId:     "testUUID",
			Type:          "testType",
			StartTime:     timeNow,
			FileExtension: "M4A",
			Status:        "testStatus",
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      "testFilePath",
		},
	}

	testMeeting := model.Meeting{
		UUID:      "testUUID",
		Topic:     "testTopic",
		StartTime: timeNow,
		Records:   testRecords,
	}

	// write a record
	err = store.SaveMeeting(testMeeting)
	assert.Nil(t, err)

	// read a record
	meeting, err := store.GetMeeting(testMeeting.UUID)
	assert.Nil(t, err)
	assert.Equal(t, testMeeting.UUID, meeting.UUID)
	assert.Equal(t, timeNow.Format(time.DateTime), meeting.DateTime)

	// read records
	records, err := store.GetRecords(testMeeting.UUID)
	assert.Nil(t, err)
	assert.Equal(t, len(testRecords), len(records))
	assert.Equal(t, timeNow.Format(time.DateTime), records[0].DateTime)
	assert.Equal(t, timeNow.Format(time.DateTime), records[1].DateTime)

	// no such meeting
	meeting, err = store.GetMeeting("noSuchUUID")
	assert.NotNil(t, err)
	assert.ErrorIs(t, err, storage.ErrNoRows)
	assert.Nil(t, meeting)

	// no such records
	records, err = store.GetRecords("noSuchUUID")
	assert.Empty(t, records)
	assert.Nil(t, err)
}
