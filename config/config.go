package config

import (
	"fmt"
	"log"
	"os"

	"gopkg.in/yaml.v3"
)

// Parameters is the main configuration struct
type Parameters struct {
	Server  Server  `yaml:"server"`
	Client  Client  `yaml:"client"`
	Storage Storage `yaml:"storage"`
}

// Client is the Zoom client configuration
type Client struct {
	AccountId string `yaml:"account_id"`
	Id        string `yaml:"id"`
	Secret    string `yaml:"secret"`
}

type Server struct {
	Listen string `yaml:"listen"` // Address or/and Port for http server to listen to
	Dbg    bool   `yaml:"dbg"`    // Debug mode
}

type Storage struct {
	// Type of storage to use
	// Currently supported: sqlite, memory
	Type       string `yaml:"type"`
	Path       string `yaml:"path"` // Path to the database file
	Repository string `yaml:"repository"`
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
