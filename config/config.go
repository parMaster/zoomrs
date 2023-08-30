package config

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/parMaster/zoomrs/storage/model"
	"gopkg.in/yaml.v3"
)

// Parameters is the main configuration struct
type Parameters struct {
	Server    Server    `yaml:"server"`    // Server configuration
	Client    Client    `yaml:"client"`    // Zoom client configuration
	Storage   Storage   `yaml:"storage"`   // Storage configuration
	Syncable  Syncable  `yaml:"syncable"`  // Syncable configuration
	Commander Commander `yaml:"commander"` // Commander configuration
}

// Client is the Zoom client configuration
type Client struct {
	AccountId              string            `yaml:"account_id"`                // Zoom account id
	Id                     string            `yaml:"id"`                        // Zoom client id
	Secret                 string            `yaml:"secret"`                    // Zoom client secret
	DeleteDownloaded       bool              `yaml:"delete_downloaded"`         // Delete downloaded files from Zoom cloud
	TrashDownloaded        bool              `yaml:"trash_downloaded"`          // Move downloaded files to trash
	DeleteSkipped          bool              `yaml:"delete_skipped"`            // Delete skipped files from Zoom cloud (the ones that are shorter than MinDuration)
	CloudCapacityHardLimit model.FileSize    `yaml:"cloud_capacity_hard_limit"` // Hard limit for cloud storage capacity (in bytes)
	RateLimitingDelay      RateLimitingDelay `yaml:"rate_limiting_delay"`       // Rate limiting delay
}

// RateLimitingDelay is the delay between requests to Zoom API
// ms between looped requests. APIs are grouped into categories with progressively longer delays
type RateLimitingDelay struct {
	Light  time.Duration
	Medium time.Duration
	Heavy  time.Duration
}

// unmarshal RateLimitingDaley fields to time.Duration
func (r *RateLimitingDelay) UnmarshalYAML(value *yaml.Node) error {
	type tmp struct {
		Light  int `yaml:"light"`
		Medium int `yaml:"medium"`
		Heavy  int `yaml:"heavy"`
	}
	var t tmp
	if err := value.Decode(&t); err != nil {
		return err
	}
	r.Light = time.Duration(t.Light) * time.Millisecond
	r.Medium = time.Duration(t.Medium) * time.Millisecond
	r.Heavy = time.Duration(t.Heavy) * time.Millisecond
	return nil
}

type Server struct {
	Listen            string   `yaml:"listen"`              // Address or/and Port for http server to listen to
	Dbg               bool     `yaml:"dbg"`                 // Debug mode
	AccessKeySalt     string   `yaml:"access_key_salt"`     // Salt for access key generation
	Domain            string   `yaml:"domain"`              // Domain name for OAuth
	OAuthClientId     string   `yaml:"oauth_client_id"`     // OAuth client id
	OAuthClientSecret string   `yaml:"oauth_client_secret"` // OAuth client secret
	OAuthDisableXSRF  bool     `yaml:"oauth_disable_xsrf"`  // OAuth disable XSRF setting
	JWTSecret         string   `yaml:"jwt_secret"`          // JWT secret
	Managers          []string `yaml:"managers"`            // List of managers emails
	SyncJob           bool     `yaml:"sync_job"`            // Run sync job
	DownloadJob       bool     `yaml:"download_job"`        // Run download job
}

type Storage struct {
	Type          string `yaml:"type"`            // Type of storage to use. Currently supported: sqlite
	Path          string `yaml:"path"`            // Path to the database file
	Repository    string `yaml:"repository"`      // Path to the repository folder where downloaded files are stored
	KeepFreeSpace uint64 `yaml:"keep_free_space"` // Keep at least this amount of free space (in bytes) on the local storage
}

type Syncable struct {
	Important   []string `yaml:"important"`    // Sync types important to download
	Alternative []string `yaml:"alternative"`  // Sync types to download if important is not available
	Optional    []string `yaml:"optional"`     // Sync types to download if possible
	MinDuration int      `yaml:"min_duration"` // Minutes - Minimal duration meeting. Meetings shorter than this value will not be synced
}

type Commander struct {
	Instances []string `yaml:"instances"` // List of instances to check for download status against, before trash/deleting
}

// NewConfig creates a new Parameters from the given file
func NewConfig(fname string) (*Parameters, error) {
	p := &Parameters{}
	data, err := os.ReadFile(fname)
	if err != nil {
		log.Printf("[ERROR] can't read config %s: %e", fname, err)
		return nil, fmt.Errorf("can't read config %s: %w", fname, err)
	}
	if err = yaml.Unmarshal(data, &p); err != nil {
		log.Printf("[ERROR] failed to parse config %s: %e", fname, err)
		return nil, fmt.Errorf("failed to parse config %s: %w", fname, err)
	}
	// log.Printf("[DEBUG] config: %+v", p)
	return p, nil
}
