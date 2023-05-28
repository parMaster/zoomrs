package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/parMaster/zoomrs/config"
	"github.com/stretchr/testify/assert"
)

// go test -v ./cmd/service -run ^Test_CheckConsistency$
// check if all records have corresponding files
func Test_CheckConsistency(t *testing.T) {

	cfgPath := "../../config/config.yml"
	// check if config file exists
	if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
		fmt.Println("Config file does not exist: ", cfgPath)
		return
	}

	cfg, err := config.NewConfig(cfgPath)
	assert.NoError(t, err)

	dbPath := cfg.Storage.Path
	// cut off "file:" prefix
	dbPath = dbPath[5:]
	// cut off parameters after ? in dbPath
	if i := strings.Index(dbPath, "?"); i != -1 {
		dbPath = dbPath[:i]
	}

	// check if db file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		fmt.Println("Database file does not exist: ", dbPath)
		return
	}

	log.Println("Database file: ", dbPath)

	// return // skip this test
	sqliteDatabase, err := sql.Open("sqlite3", cfg.Storage.Path)
	assert.NoError(t, err)
	defer sqliteDatabase.Close()

	q := "select id, meetingId, startTime, path, fileSize from records WHERE status = 'downloaded' ORDER BY startTime;"
	rows, err := sqliteDatabase.Query(q)
	assert.NoError(t, err)
	defer rows.Close()

	var checked int
	for rows.Next() {
		var id string
		var meetingId string
		var startTime string
		var path string
		var fileSize int64
		rows.Scan(&id, &meetingId, &startTime, &path, &fileSize)
		// fmt.Println(id, startTime, path)

		// check if file with path exists
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Println("File does not exist: ", path)
		}
		// check if file is not empty
		if info, err := os.Stat(path); err == nil {
			if info.Size() == 0 {
				fmt.Println("File is empty: ", path)
			}
		}
		// check if file size matches record.FileSize
		if info, err := os.Stat(path); err == nil {
			if info.Size() != fileSize {
				fmt.Println("File size does not match: ", path)
			}
		}
		checked++
	}
	fmt.Println("Checked files: ", checked)
}
