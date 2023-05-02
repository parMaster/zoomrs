package sqlite

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
)

func Test_SqliteStorage(t *testing.T) {

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var err error
	store, err := NewStorage(ctx, "file:test_storage.db?mode=rwc&_journal_mode=WAL")
	if err != nil {
		log.Printf("[ERROR] Failed to open SQLite storage: %e", err)
	}
	store.Cleanup()

	testRecords := []model.Record{
		{
			Id:            "Id1",
			MeetingId:     "testUUID",
			Type:          "testType",
			StartTime:     time.Now(),
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
			StartTime:     time.Now(),
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
		StartTime: time.Now(),
		Records:   testRecords,
	}

	// write a record
	err = store.SaveMeeting(testMeeting)
	assert.Nil(t, err)

}
