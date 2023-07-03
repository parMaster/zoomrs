package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/parMaster/zoomrs/config"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
)

func setup() (*config.Parameters, error) {
	cfgPath := "../../config/config.yml"
	// check if config file exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Config file does not exist: %s", cfgPath)
	}
	cfg, err := config.NewConfig(cfgPath)

	if err != nil {
		return nil, fmt.Errorf("[ERROR] failed to load config: %e", err)
	}

	dbPath := cfg.Storage.Path
	// cut off "file:" prefix
	dbPath = dbPath[5:]
	// cut off parameters after ? in dbPath
	if i := strings.Index(dbPath, "?"); i != -1 {
		dbPath = dbPath[:i]
	}

	// check if db file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("Database file does not exist: %s", dbPath)
	}

	log.Println("Database file: ", dbPath)
	return cfg, nil
}

// go test -v ./cmd/service -run ^Test_CheckConsistency$
// check if all records have corresponding files
func Test_CheckConsistency(t *testing.T) {

	cfg, err := setup()
	if err != nil {
		t.Skip(err.Error())
	}

	var s storage.Storer
	ctx := context.Background()
	err = LoadStorage(ctx, cfg.Storage, &s)
	if err != nil {
		t.Skip(err.Error())
	}

	recs, err := s.GetRecordsByStatus(model.StatusDownloaded)
	assert.NoError(t, err)
	var checked int
	for _, rec := range recs {
		// check if file with path exists
		if _, err := os.Stat(rec.FilePath); os.IsNotExist(err) {
			fmt.Println("File does not exist: ", rec.FilePath)
		}
		// check if file is not empty
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() == 0 {
				fmt.Println("File is empty: ", rec.FilePath)
			}
		}
		// check if file size matches record.FileSize
		if info, err := os.Stat(rec.FilePath); err == nil {
			if info.Size() != int64(rec.FileSize) {
				fmt.Println("File size does not match: ", rec.FilePath)
			}
		}
		checked++
	}
	fmt.Println("Checked files: ", checked)
}
