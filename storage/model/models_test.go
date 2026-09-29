package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_CloudUsage(t *testing.T) {

	apiResponse := `{
		"date": "2023-10-01",
		"free_usage": "1.2 TB",
		"plan_usage": "0",
		"usage": "94.72 GB"
	}`

	var cloud CloudRecordingStorage
	err := json.Unmarshal([]byte(apiResponse), &cloud)
	require.NoError(t, err)

	require.Equal(t, "2023-10-01", cloud.Date)
	assert.Equal(t, FileSize(1319413953331), cloud.FreeUsage) // 1.2 TB in bytes
	assert.Equal(t, FileSize(0), cloud.PlanUsage)             // 0 in bytes
	assert.Equal(t, "0 B", cloud.PlanUsage.String())          // 0 in bytes
	assert.Equal(t, FileSize(101704825569), cloud.Usage)      // 94.7 GB in bytes

	// calculate usage percent
	if cloud.FreeUsage+cloud.PlanUsage == 0 {
		cloud.UsagePercent = 0
	} else {
		cloud.UsagePercent = int((float64(cloud.Usage) / float64(cloud.FreeUsage+cloud.PlanUsage)) * 100)
	}

	assert.Equal(t, 7, cloud.UsagePercent) // 94.72 GB is 7% of 1.2 TB

	// is FileSize Stringer interface implemented
	assert.Equal(t, "1.2 TB", FileSize(1319413953331).String())
	assert.Equal(t, "94.7 GB", FileSize(101704825569).String())
}

func TestRecordType_String(t *testing.T) {
	assert.Equal(t, "chat_file", ChatFile.String())
}

func TestRecord_Paths(t *testing.T) {
	r := Record{Id: "rec1", DateTime: "2024-03-01 10:11:12"}
	recFolder, dateFolder := r.Paths("/data")
	assert.Equal(t, "/data/2024-03-01/rec1", recFolder)
	assert.Equal(t, "/data/2024-03-01", dateFolder)
}

func TestFileSize_String(t *testing.T) {
	assert.Equal(t, "1023 B", FileSize(1023).String())
	assert.Equal(t, "1.5 kB", FileSize(1536).String())
	assert.Equal(t, "2.0 MB", FileSize(2<<20).String())
}

func TestFileSize_MarshalJSON(t *testing.T) {
	b, err := json.Marshal(struct {
		Size FileSize `json:"size"`
	}{1536})
	require.NoError(t, err)
	assert.JSONEq(t, `{"size":"1.5 kB"}`, string(b))
}

func TestFileSize_UnmarshalJSON(t *testing.T) {
	ok := map[string]FileSize{
		`42`:         42,
		`"42"`:       42,
		`"7 B"`:      7,
		`"7 bytes"`:  7,
		`"1.5 kB"`:   1536,
		`"2 MB"`:     2 << 20,
		`"3 GB"`:     3 << 30,
		`"1 TB"`:     1 << 40,
		`"0.5 gb"`:   512 << 20,
		`"  2  MB "`: 2 << 20,
	}
	for in, want := range ok {
		var f FileSize
		require.NoError(t, json.Unmarshal([]byte(in), &f), in)
		assert.Equal(t, want, f, in)
	}

	bad := map[string]string{
		`true`:     "cannot unmarshal FileSize value",
		`-5`:       "cannot unmarshal FileSize value",
		`""`:       "invalid format",
		`"abc MB"`: "cannot parse float value",
		`"1 PB"`:   "unknown unit: PB",
	}
	for in, want := range bad {
		var f FileSize
		assert.ErrorContains(t, json.Unmarshal([]byte(in), &f), want, in)
	}
}
