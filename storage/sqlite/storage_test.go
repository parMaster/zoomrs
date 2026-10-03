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

func TestScopedQueue(t *testing.T) {
	ctx := context.Background()
	day := time.Date(2024, 3, 10, 9, 0, 0, 0, time.Local)
	// uuids the way Zoom makes them: base64 with '/', '+' and '='
	const older, slashed, plain = "old+/uuid==", "/aB+c/Dd==", "plain"

	seed := func(t *testing.T) *SQLiteStorage {
		t.Helper()
		store := newTestStorage(t)
		rec := func(id, meeting string, start time.Time, status model.RecordStatus) model.Record {
			return model.Record{Id: id, MeetingId: meeting, StartTime: start, Status: status}
		}
		for _, m := range []model.Meeting{
			{UUID: older, StartTime: day.Add(-240 * time.Hour), Records: []model.Record{
				rec("oldQueued", older, day.Add(-240*time.Hour), model.StatusQueued),
				rec("oldFailed", older, day.Add(-240*time.Hour), model.StatusFailed),
				rec("oldStuck", older, day.Add(-240*time.Hour), model.StatusDownloading),
			}},
			{UUID: slashed, StartTime: day.Add(time.Hour), Records: []model.Record{
				rec("b2", slashed, day.Add(time.Hour), model.StatusQueued),
				rec("b1", slashed, day.Add(time.Hour), model.StatusQueued),
				rec("failed", slashed, day.Add(time.Hour), model.StatusFailed),
				rec("done", slashed, day.Add(time.Hour), model.StatusDownloaded),
			}},
			{UUID: plain, StartTime: day, Records: []model.Record{
				rec("a1", plain, day, model.StatusQueued),
				rec("stuck", plain, day, model.StatusDownloading),
			}},
		} {
			require.NoError(t, store.SaveMeeting(ctx, m))
		}
		return store
	}
	statuses := func(t *testing.T, store *SQLiteStorage) map[string]model.RecordStatus {
		t.Helper()
		got := map[string]model.RecordStatus{}
		for _, uuid := range []string{older, slashed, plain} {
			recs, err := store.GetRecords(ctx, uuid)
			require.NoError(t, err)
			for _, r := range recs {
				got[r.Id] = r.Status
			}
		}
		return got
	}
	untouched := map[string]model.RecordStatus{
		"oldQueued": model.StatusQueued, "oldFailed": model.StatusFailed, "oldStuck": model.StatusDownloading,
		"b2": model.StatusQueued, "b1": model.StatusQueued, "failed": model.StatusFailed, "done": model.StatusDownloaded,
		"a1": model.StatusQueued, "stuck": model.StatusDownloading,
	}

	t.Run("next queued record comes from the given meetings, oldest first, then by id", func(t *testing.T) {
		store := seed(t)
		for _, want := range []string{"a1", "b1", "b2"} {
			got, err := store.GetQueuedRecordOf(ctx, []string{slashed, plain, "no-such-meeting"})
			require.NoError(t, err)
			assert.Equal(t, want, got.Id)
			require.NoError(t, store.UpdateRecord(ctx, got.Id, model.StatusDownloaded, ""))
		}
		_, err := store.GetQueuedRecordOf(ctx, []string{slashed, plain})
		assert.ErrorIs(t, err, storage.ErrNoRows)
		assert.Equal(t, model.StatusQueued, statuses(t, store)["oldQueued"])
	})

	t.Run("a uuid with '/', '+' and '=' is matched as is", func(t *testing.T) {
		got, err := seed(t).GetQueuedRecordOf(ctx, []string{slashed})
		require.NoError(t, err)
		assert.Equal(t, "b1", got.Id)
		assert.Equal(t, slashed, got.MeetingId)
	})

	t.Run("meetings without records have nothing queued", func(t *testing.T) {
		_, err := seed(t).GetQueuedRecordOf(ctx, []string{"no-such-meeting", "x' OR '1'='1"})
		assert.ErrorIs(t, err, storage.ErrNoRows)
	})

	t.Run("an empty list has nothing queued", func(t *testing.T) {
		store := seed(t)
		for _, none := range [][]string{nil, {}} {
			got, err := store.GetQueuedRecordOf(ctx, none)
			assert.ErrorIs(t, err, storage.ErrNoRows)
			assert.Nil(t, got)
		}
	})

	t.Run("requeue touches failed and stuck records of the given meetings only", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.ResetFailedRecordsOf(ctx, []string{slashed, plain, "no-such-meeting"}))
		assert.Equal(t, map[string]model.RecordStatus{
			"oldQueued": model.StatusQueued, "oldFailed": model.StatusFailed, "oldStuck": model.StatusDownloading,
			"b2": model.StatusQueued, "b1": model.StatusQueued, "failed": model.StatusQueued, "done": model.StatusDownloaded,
			"a1": model.StatusQueued, "stuck": model.StatusQueued,
		}, statuses(t, store))
	})

	t.Run("requeue with an empty list changes nothing", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.ResetFailedRecordsOf(ctx, nil))
		require.NoError(t, store.ResetFailedRecordsOf(ctx, []string{}))
		assert.Equal(t, untouched, statuses(t, store))
	})

	t.Run("requeue of meetings without records changes nothing", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.ResetFailedRecordsOf(ctx, []string{"no-such-meeting", "x' OR '1'='1"}))
		assert.Equal(t, untouched, statuses(t, store))
	})

	t.Run("a closed database fails both", func(t *testing.T) {
		store := seed(t)
		require.NoError(t, store.DB.Close())
		assert.Error(t, store.ResetFailedRecordsOf(ctx, []string{plain}))
		_, err := store.GetQueuedRecordOf(ctx, []string{plain})
		assert.Error(t, err)
		assert.NotErrorIs(t, err, storage.ErrNoRows)
	})
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
