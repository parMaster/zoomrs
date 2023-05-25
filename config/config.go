package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Parameters is the main configuration struct
type Parameters struct {
	Server   Server   `yaml:"server"`
	Client   Client   `yaml:"client"`
	Storage  Storage  `yaml:"storage"`
	Syncable Syncable `yaml:"syncable"`
}

// Client is the Zoom client configuration
type Client struct {
	AccountId        string `yaml:"account_id"`
	Id               string `yaml:"id"`
	Secret           string `yaml:"secret"`
	DeleteDownloaded bool   `yaml:"delete_downloaded"`
	TrashDownloaded  bool   `yaml:"trash_downloaded"`
	DeleteSkipped    bool   `yaml:"delete_skipped"`
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
}

type Storage struct {
	// Type of storage to use
	// Currently supported: sqlite, memory
	Type       string `yaml:"type"`
	Path       string `yaml:"path"` // Path to the database file
	Repository string `yaml:"repository"`
}

type Syncable struct {
	// Sync types important to download
	Important []string `yaml:"important"`
	// Sync types to download if important is not available
	Alternative []string `yaml:"alternative"`
	// Sync types to download if possible
	Optional []string `yaml:"optional"`
	// Minutes - Minimal duration meeting. Meetings shorter than this value will not be synced
	MinDuration int `yaml:"min_duration"`
}

// New creates a new Parameters from the given file
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
