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
	store, err := NewStorage(ctx, "file:test_storage.db?mode=rwc")
	if err != nil {
		log.Printf("[ERROR] Failed to open SQLite storage: %e", err)
	}

	testRecord := model.Record{
		Id:     "testId",
		Type:   "testType",
		Status: "testStatus",
		Url:    "testUrl",
		Path:   "testPath",
	}

	testMeeting := model.Meeting{
		UUID:     "testUUID",
		Topic:    "testTopic",
		DateTime: time.Now().Format(time.DateTime),
		Records:  []model.Record{testRecord, testRecord},
	}

	// write a record
	err = store.SaveMeeting(&testMeeting)
	assert.Nil(t, err)
}
