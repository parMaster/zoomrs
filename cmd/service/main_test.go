package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/repo"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
)

func setup(cfg *config.Parameters, store storage.Storer) error {

	timeNow := time.Now()

	testRecords := []model.Record{
		{
			Id:            "Id1",
			MeetingId:     "testUUID",
			Type:          model.AudioOnly,
			StartTime:     timeNow,
			FileExtension: "M4A",
			FileSize:      4,
			Status:        model.StatusDownloaded,
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      cfg.Storage.Repository + "/Id1.m4a",
		},
		{
			Id:            "Id2",
			MeetingId:     "testUUID",
			Type:          "testType",
			StartTime:     timeNow,
			FileExtension: "M4A",
			FileSize:      4,
			Status:        model.StatusDownloaded,
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      cfg.Storage.Repository + "/Id2.m4a",
		},
		{
			Id:            "Id3",
			MeetingId:     "testUUID",
			Type:          model.ChatFile,
			StartTime:     timeNow,
			FileExtension: "M4A",
			FileSize:      4,
			Status:        model.StatusDownloaded,
			DownloadURL:   "testDownUrl",
			PlayURL:       "testPlayUrl",
			FilePath:      cfg.Storage.Repository + "/Id3.m4a",
		},
	}

	testMeeting := model.Meeting{
		UUID:      "testUUID",
		Id:        11122223333,
		Topic:     "testTopic",
		StartTime: timeNow,
		Records:   testRecords,
	}

	ctx := context.Background()

	err := store.SaveMeeting(ctx, testMeeting)
	if err != nil {
		return err
	}

	// create files
	for _, rec := range testRecords {
		err := os.WriteFile(rec.FilePath, []byte("test"), 0644)
		if err != nil {
			return err
		}
		// log.Printf("[DEBUG] Created file: %s", rec.FilePath)
	}

	return nil
}

// go test -v ./cmd/service -run ^Test_CheckConsistency$
// check if all records have corresponding files
func Test_CheckConsistency(t *testing.T) {

	cfgPath := "../../config/config_example.yml"
	if os.Getenv("CONFIG") != "" {
		cfgPath = os.Getenv("CONFIG")
	}
	cfg, err := config.NewConfig(cfgPath)
	assert.Nil(t, err)

	// add unix timestamp to db file name
	cfg.Storage.Path = "file:" + os.TempDir() + "/" + time.Now().Format("20060102150405") + "-zoomrs_test.db?mode=rwc&_journal_mode=WAL"
	if os.Getenv("STORE") != "" {
		cfg.Storage.Path = os.Getenv("STORE")
	}

	var s storage.Storer
	ctx := context.Background()
	err = LoadStorage(ctx, cfg.Storage, &s)
	if err != nil {
		t.Skip(err.Error())
	}

	err = setup(cfg, s)
	assert.NoError(t, err)

	client := client.NewZoomClient(cfg.Client)

	repo := repo.NewRepository(s, client, cfg)

	checked, err := repo.CheckConsistency(ctx)

	assert.NoError(t, err)
	assert.Equal(t, 3, checked)

}
