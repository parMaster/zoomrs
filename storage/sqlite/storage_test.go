package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/parMaster/zoomrs/storage"
	"github.com/parMaster/zoomrs/storage/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStorage(t *testing.T) *SQLiteStorage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	store, err := NewStorage(ctx, "file:"+filepath.Join(t.TempDir(), "test.db")+"?mode=rwc&_journal_mode=WAL")
	require.NoError(t, err)
	return store
}

func Test_SqliteStorage(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	var err error

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
	err = store.SaveMeeting(ctx, testMeeting)
	assert.Nil(t, err)

	// read a record
	meeting, err := store.GetMeeting(ctx, testMeeting.UUID)
	assert.Nil(t, err)
	assert.Equal(t, testMeeting.UUID, meeting.UUID)
	assert.Equal(t, testMeeting.Id, meeting.Id)
	assert.Equal(t, timeNow.Format(time.DateTime), meeting.DateTime)

	// read records
	records, err := store.GetRecords(ctx, testMeeting.UUID)
	assert.Nil(t, err)
	assert.Equal(t, len(testRecords), len(records))
	assert.Equal(t, timeNow.Format(time.DateTime), records[0].DateTime)
	assert.Equal(t, timeNow.Format(time.DateTime), records[1].DateTime)

	// no such meeting
	meeting, err = store.GetMeeting(ctx, "noSuchUUID")
	assert.NotNil(t, err)
	assert.ErrorIs(t, err, storage.ErrNoRows)
	assert.Nil(t, meeting)

	// no such records
	records, err = store.GetRecords(ctx, "noSuchUUID")
	assert.Empty(t, records)
	assert.Nil(t, err)

	// Get queued records - happy path
	q3, err := store.GetQueuedRecord(ctx)
	assert.NoError(t, err)
	assert.NotNil(t, q3)
	assert.Equal(t, "Id1", q3.Id)
	assert.Equal(t, testRecords[0].FileSize, q3.FileSize)

	// Update record status
	err = store.UpdateRecord(ctx, "Id1", model.StatusDownloading, "testPath")
	assert.NoError(t, err)
	err = store.UpdateRecord(ctx, "Id3", model.StatusFailed, "testPath")
	assert.NoError(t, err)

	// Get queued records - no rows
	q4, err := store.GetQueuedRecord(ctx)
	assert.ErrorIs(t, err, storage.ErrNoRows)
	assert.Nil(t, q4)

	// Reset failed records
	err = store.ResetFailedRecords(ctx)
	assert.NoError(t, err)
	// check that all records are queued
	records, err = store.GetRecords(ctx, testMeeting.UUID)
	assert.NoError(t, err)
	assert.Equal(t, len(testRecords), len(records))
	assert.Equal(t, model.StatusQueued, records[0].Status)
	assert.Equal(t, model.StatusQueued, records[1].Status)
	assert.Equal(t, model.StatusQueued, records[2].Status)

	// List meetings
	meetings, err := store.GetMeetings(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(meetings))
	assert.Equal(t, testMeeting.UUID, meetings[0].UUID)
	assert.Equal(t, testMeeting.Id, meetings[0].Id)
	assert.Equal(t, timeNow.Format(time.DateTime), meetings[0].DateTime)
}

func record(id, meetingID, ext string, start time.Time, status model.RecordStatus, size model.FileSize) model.Record {
	return model.Record{Id: id, MeetingId: meetingID, Type: model.SharedScreenWithGalleryView, StartTime: start,
		FileExtension: ext, FileSize: size, Status: status}
}

func meetingUUIDs(meetings []model.Meeting) []string {
	uuids := []string{}
	for _, m := range meetings {
		uuids = append(uuids, m.UUID)
	}
	return uuids
}

func TestSaveMeeting(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	t.Run("record without status is queued", func(t *testing.T) {
		store := newTestStorage(t)
		require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1", StartTime: now,
			Records: []model.Record{record("r1", "m1", "MP4", now, "", 1)}}))
		recs, err := store.GetRecords(ctx, "m1")
		require.NoError(t, err)
		require.Len(t, recs, 1)
		assert.Equal(t, model.StatusQueued, recs[0].Status)
	})

	t.Run("duplicate meeting is an error", func(t *testing.T) {
		store := newTestStorage(t)
		m := model.Meeting{UUID: "m1", StartTime: now}
		require.NoError(t, store.SaveMeeting(ctx, m))
		assert.Error(t, store.SaveMeeting(ctx, m))
	})

	t.Run("duplicate record is an error", func(t *testing.T) {
		store := newTestStorage(t)
		require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1", StartTime: now,
			Records: []model.Record{record("r1", "m1", "MP4", now, "", 1)}}))
		assert.Error(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m2", StartTime: now,
			Records: []model.Record{record("r1", "m2", "MP4", now, "", 1)}}))
	})
}

func TestListMeetings_OnlyMeetingsWithDownloadedVideo(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	t0 := time.Now().Add(-time.Hour)
	for _, m := range []model.Meeting{
		{UUID: "video", StartTime: t0, Records: []model.Record{
			record("v1", "video", "MP4", t0, model.StatusDownloaded, 1),
			record("v2", "video", "MP4", t0, model.StatusDownloaded, 1), // two videos, still listed once
		}},
		{UUID: "queued-video", StartTime: t0, Records: []model.Record{record("q1", "queued-video", "MP4", t0, model.StatusQueued, 1)}},
		{UUID: "audio-only", StartTime: t0, Records: []model.Record{record("a1", "audio-only", "M4A", t0, model.StatusDownloaded, 1)}},
		{UUID: "newer-video", StartTime: t0.Add(time.Minute), Records: []model.Record{
			record("n1", "newer-video", "MP4", t0, model.StatusDownloaded, 1),
		}},
	} {
		require.NoError(t, store.SaveMeeting(ctx, m))
	}

	got, err := store.ListMeetings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"newer-video", "video"}, meetingUUIDs(got))
}

func TestDeleteMeeting_RemovesItsRecords(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	now := time.Now()
	require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1", StartTime: now,
		Records: []model.Record{record("r1", "m1", "MP4", now, "", 1)}}))
	require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m2", StartTime: now,
		Records: []model.Record{record("r2", "m2", "MP4", now, "", 1)}}))

	require.NoError(t, store.DeleteMeeting(ctx, "m1"))

	_, err := store.GetMeeting(ctx, "m1")
	assert.ErrorIs(t, err, storage.ErrNoRows)
	recs, err := store.GetRecords(ctx, "m1")
	require.NoError(t, err)
	assert.Empty(t, recs)
	recs, err = store.GetRecords(ctx, "m2")
	require.NoError(t, err)
	assert.Len(t, recs, 1)
}

func TestGetRecordsByStatus(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	now := time.Now()
	require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1", StartTime: now, Records: []model.Record{
		record("later", "m1", "MP4", now.Add(time.Minute), model.StatusDownloaded, 1),
		record("earlier", "m1", "MP4", now, model.StatusDownloaded, 1),
		record("queued", "m1", "MP4", now, model.StatusQueued, 1),
	}}))

	got, err := store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "earlier", got[0].Id)
	assert.Equal(t, "later", got[1].Id)

	// no match comes back as a nil slice, not an empty one (repo.GetStats trips over this)
	got, err = store.GetRecordsByStatus(ctx, model.StatusFailed)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestStats(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)

	empty, err := store.Stats(ctx)
	require.NoError(t, err)
	assert.Empty(t, empty)
	assert.NotNil(t, empty)

	now := time.Now()
	require.NoError(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1", StartTime: now, Records: []model.Record{
		record("d1", "m1", "MP4", now, model.StatusDownloaded, 1<<30),
		record("d2", "m1", "MP4", now, model.StatusDownloaded, 512<<20),
		record("q1", "m1", "MP4", now, model.StatusQueued, 3<<20),
	}}))

	stats, err := store.Stats(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[model.RecordStatus]any{
		model.StatusDownloaded: map[string]any{"size_mb": 1536, "size_gb": 1, "count": 2},
		model.StatusQueued:     map[string]any{"size_mb": 3, "size_gb": 0, "count": 1},
	}, stats)
}

func TestNewStorage_BadPath(t *testing.T) {
	_, err := NewStorage(context.Background(), "file:"+filepath.Join(t.TempDir(), "no-such-dir", "x.db")+"?mode=rwc")
	assert.Error(t, err)
}

func TestNewStorage_ClosesOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store, err := NewStorage(ctx, "file:"+filepath.Join(t.TempDir(), "x.db")+"?mode=rwc")
	require.NoError(t, err)
	cancel()
	assert.Eventually(t, func() bool { return store.DB.Ping() != nil }, 5*time.Second, 10*time.Millisecond)
}

func TestClosedDatabase_EveryCallFails(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	require.NoError(t, store.DB.Close())

	assert.Error(t, store.SaveMeeting(ctx, model.Meeting{UUID: "m1"}))
	_, err := store.GetMeeting(ctx, "m1")
	assert.Error(t, err)
	assert.NotErrorIs(t, err, storage.ErrNoRows)
	_, err = store.GetRecords(ctx, "m1")
	assert.Error(t, err)
	_, err = store.GetMeetings(ctx)
	assert.Error(t, err)
	_, err = store.ListMeetings(ctx)
	assert.Error(t, err)
	assert.Error(t, store.DeleteMeeting(ctx, "m1"))
	assert.Error(t, store.UpdateRecord(ctx, "r1", model.StatusFailed, ""))
	assert.Error(t, store.ResetFailedRecords(ctx))
	_, err = store.GetQueuedRecord(ctx)
	assert.Error(t, err)
	assert.NotErrorIs(t, err, storage.ErrNoRows)
	_, err = store.GetRecordsByStatus(ctx, model.StatusQueued)
	assert.Error(t, err)
	_, err = store.Stats(ctx)
	assert.Error(t, err)
	assert.Error(t, store.Cleanup(ctx))
}

func TestCorruptRows_FailToScan(t *testing.T) {
	ctx := context.Background()
	store := newTestStorage(t)
	// columns are loosely typed in sqlite, so text lands in integer columns and fails on scan
	_, err := store.DB.ExecContext(ctx, `INSERT INTO meetings VALUES ('m1', 'not-a-number', 't', '2024-01-01 10:00:00')`)
	require.NoError(t, err)
	_, err = store.DB.ExecContext(ctx, `INSERT INTO records VALUES
		('r1', 'm1', 'type', '2024-01-01 10:00:00', 'MP4', 'not-a-number', '', '', 'downloaded', ''),
		('r2', 'm1', 'type', '2024-01-01 10:00:00', 'MP4', 1, '', '', 'queued', ''),
		('a3', 'm1', 'type', '2024-01-01 10:00:00', 'MP4', 'x', '', '', 'queued', ''),
		('r4', 'm1', 'type', '2024-01-01 10:00:00', 'MP4', 1, '', '', NULL, '')`)
	require.NoError(t, err)

	_, err = store.GetMeeting(ctx, "m1")
	assert.Error(t, err)
	_, err = store.GetMeetings(ctx)
	assert.Error(t, err)
	_, err = store.ListMeetings(ctx)
	assert.Error(t, err)
	_, err = store.GetRecords(ctx, "m1")
	assert.Error(t, err)
	_, err = store.GetRecordsByStatus(ctx, model.StatusDownloaded)
	assert.Error(t, err)
	_, err = store.Stats(ctx)
	assert.Error(t, err)
	_, err = store.GetQueuedRecord(ctx)
	assert.Error(t, err)
}
