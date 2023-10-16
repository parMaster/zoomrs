package bolt

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
)

var (
	timeNow = time.Now()
)

func getTestMeeting() *model.Meeting {

	return &model.Meeting{
		UUID:      "testUUID",
		Id:        1234567890,
		Topic:     "testTopic",
		StartTime: timeNow,
	}

}

func getTestRecords() model.Records {

	return model.Records{
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

}

func Test_Bolt(t *testing.T) {

	ctx := context.Background()

	db, err := NewBoltDB(ctx, "../../.tmp/bbolt.db")
	assert.NoError(t, err)
	assert.NotNil(t, db)

	meeting := getTestMeeting()

	err = db.SaveMeeting(*meeting)
	assert.NoError(t, err)

	gotMeeting, err := db.GetMeeting(meeting.UUID)
	assert.NoError(t, err)
	assert.Equal(t, meeting.Id, gotMeeting.Id)
	assert.Equal(t, meeting.Topic, gotMeeting.Topic)
	assert.Equal(t, meeting.StartTime, gotMeeting.StartTime)
	assert.Equal(t, meeting.DateTime, gotMeeting.DateTime)
	assert.Equal(t, meeting.Duration, gotMeeting.Duration)
	assert.Equal(t, meeting.AccessKey, gotMeeting.AccessKey)

	log.Println("gotMeeting:", gotMeeting)

	// records := getTestRecords()
	// meeting.Records = records

	// err = db.DB.Update(func(tx *bolt.Tx) error {
	// 	b := tx.Bucket([]byte("MyBucket"))
	// 	err := b.Put([]byte("answer"), []byte("42"))
	// 	return err
	// })
	// assert.NoError(t, err)

	// err = db.DB.View(func(tx *bolt.Tx) error {
	// 	b := tx.Bucket([]byte("MyBucket"))
	// 	v := b.Get([]byte("answer"))
	// 	assert.Equal(t, "42", string(v))
	// 	return nil
	// })

}
