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
			Type:          model.AudioOnly,
			StartTime:     timeNow,
			FileExtension: "M4A",
			FileSize:      1000000000,
			Status:        model.StatusQueued,
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
			FileSize:      2000000000,
			Status:        model.StatusDownloading,
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      "testFilePath",
		},
		{
			Id:            "Id3",
			MeetingId:     "testUUID",
			Type:          model.ChatFile,
			StartTime:     timeNow,
			FileExtension: "M4A",
			FileSize:      3000000000,
			Status:        model.StatusQueued,
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      "testFilePath",
		},
	}

	testMeeting := model.Meeting{
		UUID:      "testUUID",
		Id:        11122223333,
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
	assert.Equal(t, testMeeting.Id, meeting.Id)
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

	// Get queued records - happy path
	q3, err := store.GetQueuedRecord()
	assert.NoError(t, err)
	assert.NotNil(t, q3)
	assert.Equal(t, "Id1", q3.Id)
	assert.Equal(t, testRecords[0].FileSize, q3.FileSize)

	// Update record status
	err = store.UpdateRecord("Id1", model.StatusDownloading, "testPath")
	assert.NoError(t, err)
	err = store.UpdateRecord("Id3", model.StatusFailed, "testPath")
	assert.NoError(t, err)

	// Get queued records - no rows
	q4, err := store.GetQueuedRecord()
	assert.ErrorIs(t, err, storage.ErrNoRows)
	assert.Nil(t, q4)

	// Reset failed records
	err = store.ResetFailedRecords()
	assert.NoError(t, err)
	// check that all records are queued
	records, err = store.GetRecords(testMeeting.UUID)
	assert.NoError(t, err)
	assert.Equal(t, len(testRecords), len(records))
	assert.Equal(t, model.StatusQueued, records[0].Status)
	assert.Equal(t, model.StatusQueued, records[1].Status)
	assert.Equal(t, model.StatusQueued, records[2].Status)

	// List meetings
	meetings, err := store.GetMeetings()
	assert.NoError(t, err)
	assert.Equal(t, 1, len(meetings))
	assert.Equal(t, testMeeting.UUID, meetings[0].UUID)
	assert.Equal(t, testMeeting.Id, meetings[0].Id)
	assert.Equal(t, timeNow.Format(time.DateTime), meetings[0].DateTime)
}
