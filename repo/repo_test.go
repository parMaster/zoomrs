package repo

import (
	"context"
	"log"
	"os"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/client"
	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/parMaster/zoomrs/storage/sqlite"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/stretchr/testify/assert"
)

func Test_FreeUpSpace(t *testing.T) {
	// Setting the things up

	cfgPath := "../config/config_dbg.yml"
	// check if config file exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		t.Skip("Config file does not exist: " + cfgPath)
	}
	cfg, err := config.NewConfig(cfgPath)
	if err != nil {
		t.Skip("Failed to load config: " + cfgPath)
	}

	cfg.Storage.Path = "file:../.tmp/repo_test_storage.db?mode=rwc&_journal_mode=WAL"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg.Storage.Repository = "../.tmp/test_repository"

	store, err := sqlite.NewStorage(ctx, cfg.Storage.Path)
	if err != nil {
		t.Skip(err.Error())
	}

	client := client.NewZoomClient(cfg.Client)

	repo := NewRepository(store, client, cfg)
	repo.prepareDestination(cfg.Storage.Repository)

	// Test when there is enough free space
	store.Cleanup()
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
			FilePath:      cfg.Storage.Repository + "/Id1/Id1.m4a",
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
			FilePath:      cfg.Storage.Repository + "/Id2/Id2.m4a",
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
			FilePath:      cfg.Storage.Repository + "/Id3/Id3.m4a",
		},
	}

	for _, rec := range testRecords {
		repo.prepareDestination(cfg.Storage.Repository + "/" + rec.Id)
		os.WriteFile(rec.FilePath, []byte("test"), 0644)
	}

	testMeeting := model.Meeting{
		UUID:      "testUUID",
		Id:        11122223333,
		Topic:     "testTopic",
		StartTime: timeNow,
		Records:   testRecords,
	}

	err = store.SaveMeeting(testMeeting)
	assert.NoError(t, err)

	time.Sleep(1 * time.Second)

	// set KeepFreeSpace to total free space, after creating test files - 1 byte
	usage, err := disk.Usage(cfg.Storage.Repository)
	assert.NoError(t, err)
	log.Println("Free space before test: ", usage.Free)
	cfg.Storage.KeepFreeSpace = usage.Free - 1

	deleted, err := repo.freeUpSpace()
	assert.NoError(t, err)
	assert.Equal(t, 0, deleted)

	records, err := store.GetRecordsByStatus(model.StatusDeleted)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(records))

	// check if files are deleted
	for _, rec := range testRecords {
		_, err := os.Stat(rec.FilePath)
		assert.False(t, os.IsNotExist(err))
	}

	// Testing happy path - when there is not enough free space
	store.Cleanup()

	timeNow = time.Now()
	testRecords = []model.Record{
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
			FilePath:      cfg.Storage.Repository + "/Id1/Id1.m4a",
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
			FilePath:      cfg.Storage.Repository + "/Id2/Id2.m4a",
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
			FilePath:      cfg.Storage.Repository + "/Id3/Id3.m4a",
		},
	}

	usage, err = disk.Usage(cfg.Storage.Repository)
	assert.NoError(t, err)

	// set KeepFreeSpace to total free space, before creating test files
	cfg.Storage.KeepFreeSpace = usage.Free

	log.Println("Free space before test: ", usage.Free)

	for _, rec := range testRecords {
		repo.prepareDestination(cfg.Storage.Repository + "/" + rec.Id)
		os.WriteFile(rec.FilePath, []byte("test"), 0644)
	}

	testMeeting = model.Meeting{
		UUID:      "testUUID",
		Id:        11122223333,
		Topic:     "testTopic",
		StartTime: timeNow,
		Records:   testRecords,
	}

	err = store.SaveMeeting(testMeeting)
	assert.NoError(t, err)

	deleted, err = repo.freeUpSpace()
	assert.NoError(t, err)
	assert.Equal(t, 3, deleted)

	records, err = store.GetRecordsByStatus(model.StatusDeleted)
	assert.NoError(t, err)
	assert.Equal(t, 3, len(records))

	// check if files are deleted
	for _, rec := range testRecords {
		_, err := os.Stat(rec.FilePath)
		assert.True(t, os.IsNotExist(err))
	}

}
