package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration is a time.Duration that can be marshaled/unmarshaled from YAML strings
type Duration time.Duration

// MarshalYAML implements yaml.Marshaler
func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

// UnmarshalYAML implements yaml.Unmarshaler
func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if s == "" {
		*d = Duration(0)
		return nil
	}
	duration, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration: %w", err)
	}
	*d = Duration(duration)
	return nil
}

// Duration returns the time.Duration value
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// Config represents the main application configuration
type Config struct {
	Server           ServerConfig            `yaml:"server"`
	Sonarr           []SonarrConfig          `yaml:"sonarr"`      // Support multiple Sonarr instances
	QBittorrent      []QBittorrentConfig     `yaml:"qbittorrent"` // Support multiple qBittorrent instances
	Trackers         []TrackerConfig         `yaml:"trackers"`    // Tracker definitions
	QueueCleaners    []QueueCleanerConfig    `yaml:"queue_cleaners"`
	DownloadCleaners []DownloadCleanerConfig `yaml:"download_cleaners"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// SonarrConfig holds Sonarr connection settings
type SonarrConfig struct {
	ID      string `yaml:"id"`   // Unique identifier for this Sonarr instance
	Name    string `yaml:"name"` // Display name
	URL     string `yaml:"url"`
	APIKey  string `yaml:"api_key"`
	Enabled bool   `yaml:"enabled"`
}

// QBittorrentConfig holds qBittorrent connection settings
type QBittorrentConfig struct {
	ID       string `yaml:"id"`   // Unique identifier for this qBittorrent instance
	Name     string `yaml:"name"` // Display name
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  bool   `yaml:"enabled"`
}

// TrackerConfig defines a tracker configuration
type TrackerConfig struct {
	ID       string `yaml:"id"`        // Unique identifier for this tracker
	Name     string `yaml:"name"`      // Display name
	URLRegex string `yaml:"url_regex"` // Regex pattern to match tracker URLs (e.g., "tracker\\.tleechreload\\.org")
	Enabled  bool   `yaml:"enabled"`
}

// ClientSelection defines which clients a cleaner should use
type ClientSelection struct {
	Bittorrent []string `yaml:"bittorrent"` // Array of bittorrent client IDs (e.g., ["main", "secondary"])
	Arr        []string `yaml:"arr"`        // Array of *arr app IDs (e.g., ["sonarr-main", "radarr-main"])
}

// TrackerSelection defines which trackers a cleaner should filter on
type TrackerSelection struct {
	TrackerIDs []string `yaml:"tracker_ids"` // Array of tracker IDs to include/exclude
	FilterMode string   `yaml:"filter_mode"` // "include" or "exclude" (empty = no filtering)
}

// QueueCleanerConfig defines a queue cleaner instance
type QueueCleanerConfig struct {
	ID            string           `yaml:"id"`
	Name          string           `yaml:"name"`
	Enabled       bool             `yaml:"enabled"`
	DryRun        bool             `yaml:"dry_run"`   // If true, don't actually delete torrents
	Schedule      string           `yaml:"schedule"`  // cron-like schedule (e.g., "every 1h")
	Clients       ClientSelection  `yaml:"clients"`   // Client selection (empty arrays = use all enabled)
	MaxAge        Duration         `yaml:"max_age"`   // max age for stalled downloads (e.g., "24h")
	MinSpeed      int64            `yaml:"min_speed"` // minimum speed in bytes/sec
	RemoveFailed  bool             `yaml:"remove_failed"`
	RemoveStalled bool             `yaml:"remove_stalled"`
	RemoveSlow    bool             `yaml:"remove_slow"`
	Trackers      TrackerSelection `yaml:"trackers"` // Tracker filtering configuration
}

// DownloadCleanerConfig defines a download cleaner instance
type DownloadCleanerConfig struct {
	ID             string           `yaml:"id"`
	Name           string           `yaml:"name"`
	Enabled        bool             `yaml:"enabled"`
	DryRun         bool             `yaml:"dry_run"`          // If true, don't actually delete torrents
	Schedule       string           `yaml:"schedule"`         // cron-like schedule (e.g., "every 1h")
	Clients        ClientSelection  `yaml:"clients"`          // Client selection (empty arrays = use all enabled)
	MaxRatio       float64          `yaml:"max_ratio"`        // maximum seeding ratio
	MinSeedingTime Duration         `yaml:"min_seeding_time"` // minimum seeding time (e.g., "7d")
	MaxSeedingTime Duration         `yaml:"max_seeding_time"` // maximum seeding time (e.g., "30d")
	Trackers       TrackerSelection `yaml:"trackers"`         // Tracker filtering configuration
}

// Load reads configuration from a file
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config file: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return &cfg, nil
}

// Save writes configuration to a file
func (c *Config) Save(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}

// Validate checks that the configuration is valid
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}

	// Validate Sonarr instances
	sonarrIDs := make(map[string]bool)
	for i, s := range c.Sonarr {
		if s.Enabled {
			if s.ID == "" {
				return fmt.Errorf("sonarr instance %d: id is required when enabled", i)
			}
			if s.URL == "" {
				return fmt.Errorf("sonarr instance %d (%s): URL is required when enabled", i, s.ID)
			}
			if s.APIKey == "" {
				return fmt.Errorf("sonarr instance %d (%s): API key is required when enabled", i, s.ID)
			}
			if sonarrIDs[s.ID] {
				return fmt.Errorf("sonarr instance %d: duplicate id '%s'", i, s.ID)
			}
			sonarrIDs[s.ID] = true
		}
	}

	// Validate qBittorrent instances
	qbtIDs := make(map[string]bool)
	for i, qbt := range c.QBittorrent {
		if qbt.Enabled {
			if qbt.ID == "" {
				return fmt.Errorf("qbittorrent instance %d: id is required when enabled", i)
			}
			if qbt.URL == "" {
				return fmt.Errorf("qbittorrent instance %d (%s): URL is required when enabled", i, qbt.ID)
			}
			if qbtIDs[qbt.ID] {
				return fmt.Errorf("qbittorrent instance %d: duplicate id '%s'", i, qbt.ID)
			}
			qbtIDs[qbt.ID] = true
		}
	}

	// Validate trackers
	trackerIDs := make(map[string]bool)
	for i, t := range c.Trackers {
		if t.Enabled {
			if t.ID == "" {
				return fmt.Errorf("tracker %d: id is required when enabled", i)
			}
			if t.URLRegex == "" {
				return fmt.Errorf("tracker %d (%s): url_regex is required when enabled", i, t.ID)
			}
			if trackerIDs[t.ID] {
				return fmt.Errorf("tracker %d: duplicate id '%s'", i, t.ID)
			}
			trackerIDs[t.ID] = true
		}
	}

	// Validate cleaners reference valid client IDs and tracker IDs
	for i, qc := range c.QueueCleaners {
		if err := qc.Validate(sonarrIDs, qbtIDs, trackerIDs); err != nil {
			return fmt.Errorf("queue cleaner %d (%s): %w", i, qc.ID, err)
		}
	}

	for i, dc := range c.DownloadCleaners {
		if err := dc.Validate(sonarrIDs, qbtIDs, trackerIDs); err != nil {
			return fmt.Errorf("download cleaner %d (%s): %w", i, dc.ID, err)
		}
	}

	return nil
}

// Validate checks that a queue cleaner config is valid
func (qc *QueueCleanerConfig) Validate(sonarrIDs, qbtIDs, trackerIDs map[string]bool) error {
	if qc.ID == "" {
		return fmt.Errorf("id is required")
	}
	if qc.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}
	if qc.Trackers.FilterMode != "" && qc.Trackers.FilterMode != "include" && qc.Trackers.FilterMode != "exclude" {
		return fmt.Errorf("trackers.filter_mode must be 'include' or 'exclude'")
	}

	// Validate bittorrent client IDs
	if len(qc.Clients.Bittorrent) > 0 {
		for _, id := range qc.Clients.Bittorrent {
			if !qbtIDs[id] {
				return fmt.Errorf("bittorrent client id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Validate arr app IDs
	if len(qc.Clients.Arr) > 0 {
		for _, id := range qc.Clients.Arr {
			if !sonarrIDs[id] {
				return fmt.Errorf("arr app id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Validate tracker IDs
	if len(qc.Trackers.TrackerIDs) > 0 {
		for _, id := range qc.Trackers.TrackerIDs {
			if !trackerIDs[id] {
				return fmt.Errorf("tracker id '%s' does not exist or is not enabled", id)
			}
		}
	}

	return nil
}

// Validate checks that a download cleaner config is valid
func (dc *DownloadCleanerConfig) Validate(sonarrIDs, qbtIDs, trackerIDs map[string]bool) error {
	if dc.ID == "" {
		return fmt.Errorf("id is required")
	}
	if dc.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}
	if dc.Trackers.FilterMode != "" && dc.Trackers.FilterMode != "include" && dc.Trackers.FilterMode != "exclude" {
		return fmt.Errorf("trackers.filter_mode must be 'include' or 'exclude'")
	}

	// Validate bittorrent client IDs
	if len(dc.Clients.Bittorrent) > 0 {
		for _, id := range dc.Clients.Bittorrent {
			if !qbtIDs[id] {
				return fmt.Errorf("bittorrent client id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Validate arr app IDs
	if len(dc.Clients.Arr) > 0 {
		for _, id := range dc.Clients.Arr {
			if !sonarrIDs[id] {
				return fmt.Errorf("arr app id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Validate tracker IDs
	if len(dc.Trackers.TrackerIDs) > 0 {
		for _, id := range dc.Trackers.TrackerIDs {
			if !trackerIDs[id] {
				return fmt.Errorf("tracker id '%s' does not exist or is not enabled", id)
			}
		}
	}

	return nil
}

// GetSonarrByID returns a Sonarr config by ID, or the first enabled one if ID is empty
func (c *Config) GetSonarrByID(id string) *SonarrConfig {
	if id == "" {
		// Return first enabled Sonarr
		for i := range c.Sonarr {
			if c.Sonarr[i].Enabled {
				return &c.Sonarr[i]
			}
		}
		return nil
	}
	for i := range c.Sonarr {
		if c.Sonarr[i].ID == id && c.Sonarr[i].Enabled {
			return &c.Sonarr[i]
		}
	}
	return nil
}

// GetQBittorrentByID returns a qBittorrent config by ID, or the first enabled one if ID is empty
func (c *Config) GetQBittorrentByID(id string) *QBittorrentConfig {
	if id == "" {
		// Return first enabled qBittorrent
		for i := range c.QBittorrent {
			if c.QBittorrent[i].Enabled {
				return &c.QBittorrent[i]
			}
		}
		return nil
	}
	for i := range c.QBittorrent {
		if c.QBittorrent[i].ID == id && c.QBittorrent[i].Enabled {
			return &c.QBittorrent[i]
		}
	}
	return nil
}

// GetTrackersByIDs returns tracker configs by their IDs
func (c *Config) GetTrackersByIDs(ids []string) []TrackerConfig {
	if len(ids) == 0 {
		// Return all enabled trackers
		var result []TrackerConfig
		for i := range c.Trackers {
			if c.Trackers[i].Enabled {
				result = append(result, c.Trackers[i])
			}
		}
		return result
	}

	var result []TrackerConfig
	for _, id := range ids {
		for i := range c.Trackers {
			if c.Trackers[i].ID == id && c.Trackers[i].Enabled {
				result = append(result, c.Trackers[i])
				break
			}
		}
	}
	return result
}
