package sqlite

import (
	"context"
	"database/sql"
	"log"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
)

type SQLiteStorage struct {
	DB  *sql.DB
	ctx context.Context
}

// NewStorage creates new SQLite storage, creates tables if they don't exist
func NewStorage(ctx context.Context, path string) (*SQLiteStorage, error) {
	sqliteDatabase, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		sqliteDatabase.Close()
	}()

	q := `CREATE TABLE IF NOT EXISTS meetings (
		uuid TEXT PRIMARY KEY,
		id INTEGER,
		topic TEXT,
		startTime TEXT
	);
	CREATE TABLE IF NOT EXISTS records (
		id TEXT PRIMARY KEY,
		meetingId TEXT,
		type TEXT,
		startTime TEXT,
		fileExtension TEXT,
		fileSize INTEGER,
		downUrl TEXT,
		playUrl TEXT,
		status TEXT,
		path TEXT
	);`
	_, err = sqliteDatabase.ExecContext(ctx, q)
	if err != nil {
		return nil, err
	}

	return &SQLiteStorage{DB: sqliteDatabase, ctx: ctx}, nil
}

// SaveMeeting saves a meeting to the database
func (s *SQLiteStorage) SaveMeeting(meeting model.Meeting) error {
	// convert time to local
	meeting.StartTime = meeting.StartTime.Local()

	q := "INSERT INTO `meetings`(uuid, id, topic, startTime) VALUES ($1, $2, $3, $4)"
	log.Printf("[DEBUG] Saving meeting: %v", meeting)

	_, err := s.DB.ExecContext(s.ctx, q,
		meeting.UUID,                            // uuid
		meeting.Id,                              // id
		meeting.Topic,                           // topic
		meeting.StartTime.Format(time.DateTime)) // startTime

	if err != nil {
		return err
	}

	for _, r := range meeting.Records {
		err := s.saveRecord(r)
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveRecord saves a record to the database
func (s *SQLiteStorage) saveRecord(record model.Record) error {
	if record.Status == "" {
		record.Status = model.Queued
	}

	// convert time to local
	record.StartTime = record.StartTime.Local()

	q := "INSERT INTO `records` VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)"
	_, err := s.DB.ExecContext(s.ctx, q,
		record.Id,                              // id
		record.MeetingId,                       // meetingId
		record.Type,                            // type
		record.StartTime.Format(time.DateTime), // startTime
		record.FileExtension,                   // fileExtension
		record.FileSize,                        // fileSize
		record.DownloadURL,                     // downUrl
		record.PlayURL,                         // playUrl
		record.Status,                          // status
		record.FilePath)                        // path
	return err
}

// GetMeeting returns a meeting from the database
func (s *SQLiteStorage) GetMeeting(UUID string) (*model.Meeting, error) {
	q := "SELECT * FROM `meetings` WHERE uuid = $1"
	row := s.DB.QueryRowContext(s.ctx, q, UUID)
	meeting := model.Meeting{}
	err := row.Scan(&meeting.UUID, &meeting.Id, &meeting.Topic, &meeting.DateTime)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNoRows
		}
		return nil, err
	}
	return &meeting, nil
}

// GetMeeting returns a meeting from the database
func (s *SQLiteStorage) GetMeetingById(Id string) (*model.Meeting, error) {
	q := "SELECT * FROM `meetings` WHERE id = $1"
	row := s.DB.QueryRowContext(s.ctx, q, Id)
	meeting := model.Meeting{}
	err := row.Scan(&meeting.UUID, &meeting.Id, &meeting.Topic, &meeting.DateTime)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNoRows
		}
		return nil, err
	}
	return &meeting, nil
}

// GetRecords returns records of specific meeting from the database
func (s *SQLiteStorage) GetRecords(UUID string) ([]model.Record, error) {
	q := "SELECT * FROM `records` WHERE meetingId = $1"
	rows, err := s.DB.QueryContext(s.ctx, q, UUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []model.Record
	for rows.Next() {
		record := model.Record{}
		err := rows.Scan(
			&record.Id,
			&record.MeetingId,
			&record.Type,
			&record.DateTime,
			&record.FileExtension,
			&record.FileSize,
			&record.DownloadURL,
			&record.PlayURL,
			&record.Status,
			&record.FilePath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// GetRecords returns records of specific meeting from the database
func (s *SQLiteStorage) GetRecordsInfo(UUID string) ([]model.RecordInfo, error) {
	q := "SELECT id, meetingId, type, startTime, fileSize, status, path FROM `records` WHERE meetingId = $1"
	rows, err := s.DB.QueryContext(s.ctx, q, UUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []model.RecordInfo
	for rows.Next() {
		record := model.RecordInfo{}
		err := rows.Scan(
			&record.Id,
			&record.MeetingId,
			&record.Type,
			&record.DateTime,
			&record.FileSize,
			&record.Status,
			&record.FilePath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// ListMeetings returns a list of meetings from the database
func (s *SQLiteStorage) ListMeetings() ([]model.Meeting, error) {
	q := "SELECT * FROM `meetings` ORDER BY startTime DESC"
	rows, err := s.DB.QueryContext(s.ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var meetings []model.Meeting
	for rows.Next() {
		meeting := model.Meeting{}
		err := rows.Scan(&meeting.UUID, &meeting.Id, &meeting.Topic, &meeting.DateTime)
		if err != nil {
			return nil, err
		}
		meetings = append(meetings, meeting)
	}
	return meetings, nil
}

// DeleteMeeting deletes a meeting with corresponding records from the database
func (s *SQLiteStorage) DeleteMeeting(UUID string) error {
	q := "DELETE FROM `records` WHERE meetingId = $1"
	_, err := s.DB.ExecContext(s.ctx, q, UUID)
	if err != nil {
		return err
	}

	q = "DELETE FROM `meetings` WHERE uuid = $1"
	_, err = s.DB.ExecContext(s.ctx, q, UUID)
	return err
}

// UpdateRecord updates a record in the database
func (s *SQLiteStorage) UpdateRecord(Id string, status model.RecordStatus, path string) error {
	q := "UPDATE `records` SET status = $1, path = $2 WHERE id = $3"
	_, err := s.DB.ExecContext(s.ctx, q, status, path, Id)
	return err
}

// ResetFailedRecords resets all failed records to queued
func (s *SQLiteStorage) ResetFailedRecords() error {
	q := "UPDATE `records` SET status = 'queued' WHERE status IN ('failed', 'downloading')"
	_, err := s.DB.ExecContext(s.ctx, q)
	return err
}

// GetQueuedRecord returns a queued record from the database
func (s *SQLiteStorage) GetQueuedRecord() (*model.Record, error) {
	q := "SELECT * FROM `records` WHERE status = $1 ORDER BY startTime, id LIMIT 1"

	row := s.DB.QueryRowContext(s.ctx, q, model.Queued)
	record := model.Record{}
	err := row.Scan(
		&record.Id,
		&record.MeetingId,
		&record.Type,
		&record.DateTime,
		&record.FileExtension,
		&record.FileSize,
		&record.DownloadURL,
		&record.PlayURL,
		&record.Status,
		&record.FilePath)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, storage.ErrNoRows
		}
		return nil, err
	}
	return &record, nil
}

// GetRecords returns records from the database
func (s *SQLiteStorage) GetRecordsByStatus(status model.RecordStatus) ([]model.Record, error) {
	q := "SELECT * FROM `records` WHERE status = $1"
	rows, err := s.DB.QueryContext(s.ctx, q, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []model.Record

	for rows.Next() {
		record := model.Record{}
		err := rows.Scan(
			&record.Id,
			&record.MeetingId,
			&record.Type,
			&record.DateTime,
			&record.FileExtension,
			&record.FileSize,
			&record.DownloadURL,
			&record.PlayURL,
			&record.Status,
			&record.FilePath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	return records, nil
}

// Stats returns the number of records in each status
func (s *SQLiteStorage) Stats() (map[model.RecordStatus]interface{}, error) {
	q := "select sum(fileSize)/1048576 as size_mb, sum(fileSize)/1073741824 as size_gb, count(id) as count, status FROM records group by status;"
	rows, err := s.DB.QueryContext(s.ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	stats := make(map[model.RecordStatus]interface{})
	for rows.Next() {
		var size_mb int
		var size_gb int
		var status string
		var count int
		err := rows.Scan(&size_mb, &size_gb, &count, &status)
		if err != nil {
			return nil, err
		}
		stats[model.RecordStatus(status)] = map[string]interface{}{"size_mb": size_mb, "size_gb": size_gb, "count": count}
	}
	return stats, nil
}

// Cleanup deletes all meetings and records from the database, used for testing
func (s *SQLiteStorage) Cleanup() error {
	q := "DELETE FROM `meetings`"
	_, err := s.DB.ExecContext(s.ctx, q)
	if err != nil {
		return err
	}
	q = "DELETE FROM `records`"
	_, err = s.DB.ExecContext(s.ctx, q)
	return err
}
