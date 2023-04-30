package sqlite

import (
	"context"
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
	"github.com/parMaster/zoomrs/storage/model"
)

type SQLiteStorage struct {
	DB  *sql.DB
	ctx context.Context
}

func NewStorage(ctx context.Context, path string) (*SQLiteStorage, error) {
	sqliteDatabase, err := sql.Open("sqlite3", path)
	if err != nil {
		return nil, err
	}

	go func() {
		<-ctx.Done()
		sqliteDatabase.Close()
	}()

	return &SQLiteStorage{DB: sqliteDatabase}, nil
}

// implement Storer interface

// SaveMeeting saves a meeting to the database
func (s *SQLiteStorage) SaveMeeting(meeting *model.Meeting) error {
	q := "INSERT INTO `meetings` VALUES ($1, $2, $3)"
	_, err := s.DB.ExecContext(s.ctx, q, meeting.UUID, meeting.Topic, meeting.DateTime)
	if err != nil {
		return err
	}

	for _, r := range meeting.Records {
		err := s.SaveRecord(meeting.UUID, &r)
		if err != nil {
			return err
		}
	}
	return nil
}

// SaveRecord saves a record to the database
func (s *SQLiteStorage) SaveRecord(UUID string, record *model.Record) error {
	q := "INSERT INTO `records` VALUES ($1, $2, $3, $4, $5, $6)"
	_, err := s.DB.ExecContext(s.ctx, q, record.Id, UUID, record.Type, record.Status, record.Url, record.Path)
	return err
}

// GetMeeting returns a meeting from the database
func (s *SQLiteStorage) GetMeeting(UUID string) (*model.Meeting, error) {
	q := "SELECT * FROM `meetings` WHERE UUID = $1"
	row := s.DB.QueryRowContext(s.ctx, q, UUID)
	meeting := model.Meeting{}
	err := row.Scan(&meeting.UUID, &meeting.Topic, &meeting.DateTime)
	if err != nil {
		return nil, err
	}
	return &meeting, nil
}

// ListMeetings returns a list of meetings from the database
func (s *SQLiteStorage) ListMeetings() ([]model.Meeting, error) {
	q := "SELECT * FROM `meetings`"
	rows, err := s.DB.QueryContext(s.ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var meetings []model.Meeting
	for rows.Next() {
		meeting := model.Meeting{}
		err := rows.Scan(&meeting.UUID, &meeting.Topic, &meeting.DateTime)
		if err != nil {
			return nil, err
		}
		meetings = append(meetings, meeting)
	}
	return meetings, nil
}

// DeleteMeeting deletes a meeting from the database
func (s *SQLiteStorage) DeleteMeeting(UUID string) error {
	q := "DELETE FROM `meetings` WHERE UUID = $1"
	_, err := s.DB.ExecContext(s.ctx, q, UUID)
	return err
}

// UpdateRecord updates a record in the database
func (s *SQLiteStorage) UpdateRecord(Id string, status string) error {
	q := "UPDATE `records` SET Status = $1 WHERE Id = $2"
	_, err := s.DB.ExecContext(s.ctx, q, status, Id)
	return err
}

// func (s *SQLiteStorage) Write(ctx context.Context, d model.Data) error {

// 	if ok, err := s.moduleActive(ctx, d.Module); err != nil || !ok {
// 		return err
// 	}

// 	if d.DateTime == "" {
// 		d.DateTime = time.Now().Format("2006-01-02 15:04")
// 	}

// 	if d.Topic == "" {
// 		return errors.New("topic is empty")
// 	}

// 	q := fmt.Sprintf("INSERT INTO `%s` VALUES ($1, $2, $3)", d.Module)

// 	_, err := s.DB.ExecContext(ctx, q, d.DateTime, d.Topic, d.Value)
// 	return err
// }

// Read reads records for the given module from the database
// func (s *SQLiteStorage) Read(ctx context.Context, module string) (data []model.Data, err error) {

// 	q := fmt.Sprintf("SELECT * FROM `%s`", module)
// 	rows, err := s.DB.QueryContext(ctx, q)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer rows.Close()

// 	for rows.Next() {
// 		d := model.Data{Module: module}
// 		err = rows.Scan(&d.DateTime, &d.Topic, &d.Value)
// 		if err != nil {
// 			return nil, err
// 		}
// 		data = append(data, d)
// 	}

// 	return
// }

// View returns a map of topics and their values for the given module
// The map is sorted by DateTime and structured as follows:
// map[Topic]map[DateTime]Value
// func (s *SQLiteStorage) View(ctx context.Context, module string) (data map[string]map[string]string, err error) {

// 	data = make(map[string]map[string]string)

// 	// select distinct topics from module
// 	q := fmt.Sprintf("SELECT DISTINCT Topic FROM `%s`", module)
// 	rows, err := s.DB.QueryContext(ctx, q)
// 	if err != nil {
// 		return nil, err
// 	}
// 	defer rows.Close()

// 	for rows.Next() {
// 		var topic string
// 		err = rows.Scan(&topic)
// 		if err != nil {
// 			return nil, err
// 		}
// 		data[topic] = make(map[string]string)
// 	}

// 	// select all records from module and fill the map
// 	q = fmt.Sprintf("SELECT * FROM `%s` ORDER BY DateTime", module)
// 	rows, err = s.DB.QueryContext(ctx, q)
// 	if err != nil {
// 		return nil, err
// 	}

// 	for rows.Next() {
// 		d := model.Data{Module: module}
// 		err = rows.Scan(&d.DateTime, &d.Topic, &d.Value)
// 		if err != nil {
// 			return nil, err
// 		}
// 		data[d.Topic][d.DateTime] = d.Value
// 	}

// 	return
// }

// // Check if the table exists, create if not. Cache the result in the map
// func (s *SQLiteStorage) moduleActive(ctx context.Context, module string) (bool, error) {

// 	if module == "" {
// 		return false, errors.New("module name is empty")
// 	}

// 	if s.activeModules[module] {
// 		return true, nil
// 	}

// 	if _, ok := s.activeModules[module]; !ok {
// 		q := fmt.Sprintf("CREATE TABLE IF NOT EXISTS `%s` (DateTime TEXT, Topic TEXT, Value TEXT)", module)
// 		_, err := s.DB.ExecContext(ctx, q)
// 		if err != nil {
// 			return false, err
// 		}
// 		s.activeModules[module] = true
// 	}

// 	return true, nil
// }
