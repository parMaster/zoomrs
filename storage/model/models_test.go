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
	assert.Equal(t, FileSize(101704825569), cloud.Usage)      // 94.72 GB in bytes

	// calculate usage percent
	if cloud.FreeUsage+cloud.PlanUsage == 0 {
		cloud.UsagePercent = 0
	} else {
		cloud.UsagePercent = int((float64(cloud.Usage) / float64(cloud.FreeUsage+cloud.PlanUsage)) * 100)
	}

	assert.Equal(t, 7, cloud.UsagePercent) // 94.72 GB is 7% of 1.2 TB
}
