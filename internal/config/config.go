package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"
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

// DataSize represents a data size in bytes that can be marshaled/unmarshaled from YAML strings
// Supports formats like "1MiB", "1MB", "1024KB", "1048576B", etc.
type DataSize int64

// MarshalYAML implements yaml.Marshaler
func (ds DataSize) MarshalYAML() (interface{}, error) {
	return ds.String(), nil
}

// String returns a human-readable representation of the data size
func (ds DataSize) String() string {
	bytes := int64(ds)
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%dB", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB"}
	return fmt.Sprintf("%.1f%s", float64(bytes)/float64(div), units[exp])
}

// UnmarshalYAML implements yaml.Unmarshaler
func (ds *DataSize) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		// Try parsing as integer (backward compatibility)
		var i int64
		if err2 := value.Decode(&i); err2 == nil {
			*ds = DataSize(i)
			return nil
		}
		return err
	}
	if s == "" {
		*ds = DataSize(0)
		return nil
	}

	// Parse human-readable size (e.g., "1MiB", "1MB", "1024KB")
	size, err := parseDataSize(s)
	if err != nil {
		return fmt.Errorf("invalid data size: %w", err)
	}
	*ds = DataSize(size)
	return nil
}

// Bytes returns the size in bytes
func (ds DataSize) Bytes() int64 {
	return int64(ds)
}

// parseDataSize parses a human-readable data size string
// Supports: B, KB, MB, GB, TB, PB (decimal) and KiB, MiB, GiB, TiB, PiB (binary)
func parseDataSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}

	// Remove any spaces and convert to lowercase for matching
	sLower := strings.ToLower(s)

	// Try to find the unit
	var multiplier int64 = 1
	var numStr string

	// Binary units (KiB, MiB, GiB, TiB, PiB)
	binaryUnits := map[string]int64{
		"kib": 1024,
		"mib": 1024 * 1024,
		"gib": 1024 * 1024 * 1024,
		"tib": 1024 * 1024 * 1024 * 1024,
		"pib": 1024 * 1024 * 1024 * 1024 * 1024,
	}

	// Decimal units (KB, MB, GB, TB, PB)
	decimalUnits := map[string]int64{
		"kb": 1000,
		"mb": 1000 * 1000,
		"gb": 1000 * 1000 * 1000,
		"tb": 1000 * 1000 * 1000 * 1000,
		"pb": 1000 * 1000 * 1000 * 1000 * 1000,
	}

	// Check for binary units first (more common in torrenting)
	for unit, mult := range binaryUnits {
		if strings.HasSuffix(sLower, unit) {
			numStr = s[:len(s)-len(unit)]
			multiplier = mult
			break
		}
	}

	// If no binary unit found, check decimal units
	if multiplier == 1 {
		for unit, mult := range decimalUnits {
			if strings.HasSuffix(sLower, unit) {
				numStr = s[:len(s)-len(unit)]
				multiplier = mult
				break
			}
		}
	}

	// If no unit found, check for "B" suffix
	if multiplier == 1 {
		if strings.HasSuffix(sLower, "b") && len(sLower) > 1 {
			numStr = s[:len(s)-1]
			multiplier = 1
		} else {
			// No unit, assume bytes
			numStr = s
			multiplier = 1
		}
	}

	// Parse the number
	var num float64
	_, err := fmt.Sscanf(strings.TrimSpace(numStr), "%f", &num)
	if err != nil {
		return 0, fmt.Errorf("invalid number: %s", numStr)
	}

	return int64(float64(multiplier) * num), nil
}

// Config represents the main application configuration
type Config struct {
	Server            ServerConfig             `yaml:"server"`
	Arrs              []ArrConfig              `yaml:"arrs"`               // Array of *arr apps (Sonarr, Radarr, etc.)
	BittorrentClients []BittorrentClientConfig `yaml:"bittorrent_clients"` // Array of bittorrent clients (qBittorrent, etc.)
	Trackers          []TrackerConfig          `yaml:"trackers"`           // Tracker definitions
	MalwareBlocker    MalwareBlockerConfig     `yaml:"malware_blocker"`    // Malware blocker configuration
	QueueCleaners     []QueueCleanerConfig     `yaml:"queue_cleaners"`
	DownloadCleaners  []DownloadCleanerConfig  `yaml:"download_cleaners"`
}

// ServerConfig holds server configuration
type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// ArrConfig holds *arr app connection settings (Sonarr, Radarr, Lidarr, etc.)
type ArrConfig struct {
	Kind             string `yaml:"kind"` // Type of *arr app: "sonarr", "radarr", "lidarr", etc.
	ID               string `yaml:"id"`   // Unique identifier for this instance
	Name             string `yaml:"name"` // Display name
	URL              string `yaml:"url"`
	APIKey           string `yaml:"api_key"`
	Enabled          bool   `yaml:"enabled"`
	RemoveFromClient bool   `yaml:"remove_from_client"` // If true, remove from download client; if false, ignore download (default: false)
	Blocklist        bool   `yaml:"blocklist"`          // If true, blocklist the release to prevent re-download (default: false)
}

// BittorrentClientConfig holds bittorrent client connection settings (qBittorrent, etc.)
type BittorrentClientConfig struct {
	Kind     string `yaml:"kind"` // Type of bittorrent client: "qbittorrent", etc.
	ID       string `yaml:"id"`   // Unique identifier for this instance
	Name     string `yaml:"name"` // Display name
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  bool   `yaml:"enabled"`
}

// SonarrConfig holds Sonarr connection settings (deprecated, use ArrConfig with kind="sonarr")
type SonarrConfig struct {
	ID      string `yaml:"id"`
	Name    string `yaml:"name"`
	URL     string `yaml:"url"`
	APIKey  string `yaml:"api_key"`
	Enabled bool   `yaml:"enabled"`
}

// QBittorrentConfig holds qBittorrent connection settings (deprecated, use BittorrentClientConfig with kind="qbittorrent")
type QBittorrentConfig struct {
	ID       string `yaml:"id"`
	Name     string `yaml:"name"`
	URL      string `yaml:"url"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Enabled  bool   `yaml:"enabled"`
}

// TrackerConfig defines a tracker configuration
type TrackerConfig struct {
	ID                 string   `yaml:"id"`                   // Unique identifier for this tracker
	Name               string   `yaml:"name"`                 // Display name
	URLRegex           string   `yaml:"url_regex"`            // Regex pattern to match tracker URLs (e.g., "tracker\\.tleechreload\\.org")
	MinProgress        float64  `yaml:"min_progress"`         // Minimum progress percentage (0-100) before allowing deletion from bittorrent client
	SafeDeleteProgress float64  `yaml:"safe_delete_progress"` // Maximum progress percentage (0-100) below which deletion is safe (won't count as hit-and-run). If set, allows deletion even when cleaner rule has delete: false
	MinRatio           float64  `yaml:"min_ratio"`            // Minimum ratio that must be met before deletion is allowed (e.g., 1.0)
	MinSeedingTime     Duration `yaml:"min_seeding_time"`     // Minimum seeding time that must be met before deletion is allowed (e.g., "7d")
	Enabled            bool     `yaml:"enabled"`
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

// MalwareBlockerConfig defines malware blocker settings
type MalwareBlockerConfig struct {
	Enabled  bool             `yaml:"enabled"`  // Enable/disable malware blocker
	DryRun   bool             `yaml:"dry_run"`  // If true, don't actually block files (just log what would be blocked)
	Schedule string           `yaml:"schedule"` // Schedule interval (e.g., "every 1h")
	Patterns []string         `yaml:"patterns"` // List of regex patterns to match file names/paths (e.g., ["Trailer.*", ".*sample.*", "\\.nfo$", "\\.exe$"])
	Trackers TrackerSelection `yaml:"trackers"` // Tracker filtering configuration
}

// StrikeConfig defines strike-based removal configuration
type StrikeConfig struct {
	Strikes         int      `yaml:"strikes"`           // Number of times the check can fail before action
	ResetOnProgress bool     `yaml:"reset_on_progress"` // Reset strikes when progress is made (for stalled/slow)
	DeleteTorrent   bool     `yaml:"delete_torrent"`    // Delete torrent from bittorrent client (always removed from *arr app)
	MinSpeed        DataSize `yaml:"min_speed"`         // Minimum speed (e.g., "1MiB", "1MB", "1024KB") - only used for slow config
}

// QueueCleanerConfig defines a queue cleaner instance
type QueueCleanerConfig struct {
	ID           string           `yaml:"id"`
	Name         string           `yaml:"name"`
	Enabled      bool             `yaml:"enabled"`
	DryRun       bool             `yaml:"dry_run"`       // If true, don't actually remove from arr apps
	Schedule     string           `yaml:"schedule"`      // cron-like schedule (e.g., "every 1h")
	Arrs         []string         `yaml:"arrs"`          // Array of *arr app IDs to remove downloads from (empty = use all enabled)
	Bittorrent   []string         `yaml:"bittorrent"`    // Array of bittorrent client IDs to monitor (empty = use all enabled)
	Stalled      *StrikeConfig    `yaml:"stalled"`       // Stalled torrent configuration
	Slow         *StrikeConfig    `yaml:"slow"`          // Slow download configuration (requires min_speed)
	FailedImport *StrikeConfig    `yaml:"failed_import"` // Failed import configuration
	Trackers     TrackerSelection `yaml:"trackers"`      // Tracker filtering configuration
}

// SeedingTimeConfig defines seeding time limits
type SeedingTimeConfig struct {
	Min Duration `yaml:"min"` // minimum seeding time (e.g., "7d")
	Max Duration `yaml:"max"` // maximum seeding time (e.g., "30d")
}

// DownloadCleanerConfig defines a download cleaner instance
type DownloadCleanerConfig struct {
	ID             string           `yaml:"id"`
	Name           string           `yaml:"name"`
	Enabled        bool             `yaml:"enabled"`
	DryRun         bool             `yaml:"dry_run"`          // If true, don't actually delete torrents
	Schedule       string           `yaml:"schedule"`         // cron-like schedule (e.g., "every 1h")
	Clients        ClientSelection  `yaml:"clients"`          // Client selection (empty arrays = use all enabled)
	Trackers       TrackerSelection `yaml:"trackers"`         // Tracker filtering configuration
	MaxRatio       float64          `yaml:"max_ratio"`        // Maximum ratio - clean when reached (if min requirements met)
	MaxSeedingTime Duration         `yaml:"max_seeding_time"` // Maximum seeding time - clean when reached (if min requirements met)
	DeleteFiles    bool             `yaml:"delete_files"`     // If true, delete files along with torrents
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

	// Valid arr kinds (only include kinds that are actually implemented)
	validArrKinds := map[string]bool{
		"sonarr": true,
		"radarr": true,
		"lidarr": true,
	}

	// Validate *arr apps
	arrIDs := make(map[string]bool)
	for i, arr := range c.Arrs {
		if arr.Enabled {
			if arr.Kind == "" {
				return fmt.Errorf("arr app %d: kind is required when enabled", i)
			}
			if !validArrKinds[arr.Kind] {
				validKindsList := make([]string, 0, len(validArrKinds))
				for k := range validArrKinds {
					validKindsList = append(validKindsList, k)
				}
				return fmt.Errorf("arr app %d: invalid kind '%s' (valid kinds: %s)", i, arr.Kind, strings.Join(validKindsList, ", "))
			}
			if arr.ID == "" {
				return fmt.Errorf("arr app %d (%s): id is required when enabled", i, arr.Kind)
			}
			if arr.URL == "" {
				return fmt.Errorf("arr app %d (%s, %s): URL is required when enabled", i, arr.Kind, arr.ID)
			}
			if arr.APIKey == "" {
				return fmt.Errorf("arr app %d (%s, %s): API key is required when enabled", i, arr.Kind, arr.ID)
			}
			if arrIDs[arr.ID] {
				return fmt.Errorf("arr app %d: duplicate id '%s'", i, arr.ID)
			}
			arrIDs[arr.ID] = true
		}
	}

	// Valid bittorrent client kinds
	validBittorrentClientKinds := map[string]bool{
		"qbittorrent": true,
		// Add more as they are implemented (e.g., "transmission", "deluge")
	}

	// Validate bittorrent clients
	bittorrentClientIDs := make(map[string]bool)
	for i, client := range c.BittorrentClients {
		if client.Enabled {
			if client.Kind == "" {
				return fmt.Errorf("bittorrent client %d: kind is required when enabled", i)
			}
			if !validBittorrentClientKinds[client.Kind] {
				validKindsList := make([]string, 0, len(validBittorrentClientKinds))
				for k := range validBittorrentClientKinds {
					validKindsList = append(validKindsList, k)
				}
				return fmt.Errorf("bittorrent client %d: invalid kind '%s' (valid kinds: %s)", i, client.Kind, strings.Join(validKindsList, ", "))
			}
			if client.ID == "" {
				return fmt.Errorf("bittorrent client %d (%s): id is required when enabled", i, client.Kind)
			}
			if client.URL == "" {
				return fmt.Errorf("bittorrent client %d (%s, %s): URL is required when enabled", i, client.Kind, client.ID)
			}
			if bittorrentClientIDs[client.ID] {
				return fmt.Errorf("bittorrent client %d: duplicate id '%s'", i, client.ID)
			}
			bittorrentClientIDs[client.ID] = true
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

			// Validate tracker-specific settings
			if err := t.Validate(); err != nil {
				return fmt.Errorf("tracker %d (%s): %w", i, t.ID, err)
			}
		}
	}

	// Validate Malware Blocker
	if c.MalwareBlocker.Enabled {
		if c.MalwareBlocker.Schedule == "" {
			return fmt.Errorf("malware_blocker: schedule is required when enabled")
		}
		if len(c.MalwareBlocker.Patterns) == 0 {
			return fmt.Errorf("malware_blocker: at least one pattern is required when enabled")
		}
		// Validate that all patterns are valid regex
		for i, pattern := range c.MalwareBlocker.Patterns {
			if pattern == "" {
				continue // Empty patterns are skipped at runtime
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("malware_blocker: invalid regex pattern at index %d '%s': %w", i, pattern, err)
			}
		}
		// Validate tracker IDs
		if len(c.MalwareBlocker.Trackers.TrackerIDs) > 0 {
			for _, id := range c.MalwareBlocker.Trackers.TrackerIDs {
				if !trackerIDs[id] {
					return fmt.Errorf("malware_blocker: tracker id '%s' does not exist or is not enabled", id)
				}
			}
		}
		if c.MalwareBlocker.Trackers.FilterMode != "" && c.MalwareBlocker.Trackers.FilterMode != "include" && c.MalwareBlocker.Trackers.FilterMode != "exclude" {
			return fmt.Errorf("malware_blocker: tracker filter_mode must be 'include' or 'exclude'")
		}
	}

	// Validate cleaners reference valid client IDs and tracker IDs
	for i, qc := range c.QueueCleaners {
		if err := qc.Validate(arrIDs, bittorrentClientIDs, trackerIDs); err != nil {
			return fmt.Errorf("queue cleaner %d (%s): %w", i, qc.ID, err)
		}
	}

	for i, dc := range c.DownloadCleaners {
		if err := dc.Validate(arrIDs, bittorrentClientIDs, trackerIDs); err != nil {
			return fmt.Errorf("download cleaner %d (%s): %w", i, dc.ID, err)
		}
	}

	return nil
}

// Validate checks that a queue cleaner config is valid
func (qc *QueueCleanerConfig) Validate(arrIDs, bittorrentClientIDs, trackerIDs map[string]bool) error {
	if qc.ID == "" {
		return fmt.Errorf("id is required")
	}
	if qc.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}
	if qc.Trackers.FilterMode != "" && qc.Trackers.FilterMode != "include" && qc.Trackers.FilterMode != "exclude" {
		return fmt.Errorf("trackers.filter_mode must be 'include' or 'exclude'")
	}

	// Validate strike configs
	if qc.Stalled != nil {
		if qc.Stalled.Strikes <= 0 {
			return fmt.Errorf("stalled.strikes must be greater than 0")
		}
	}
	if qc.Slow != nil {
		if qc.Slow.Strikes <= 0 {
			return fmt.Errorf("slow.strikes must be greater than 0")
		}
		if qc.Slow.MinSpeed <= 0 {
			return fmt.Errorf("slow.min_speed must be greater than 0 when slow config is enabled")
		}
	}
	if qc.FailedImport != nil {
		if qc.FailedImport.Strikes <= 0 {
			return fmt.Errorf("failed_import.strikes must be greater than 0")
		}
	}

	// Validate bittorrent client IDs
	if len(qc.Bittorrent) > 0 {
		for _, id := range qc.Bittorrent {
			if !bittorrentClientIDs[id] {
				return fmt.Errorf("bittorrent client id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Validate arr app IDs
	if len(qc.Arrs) > 0 {
		for _, id := range qc.Arrs {
			if !arrIDs[id] {
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
func (dc *DownloadCleanerConfig) Validate(arrIDs, bittorrentClientIDs, trackerIDs map[string]bool) error {
	if dc.ID == "" {
		return fmt.Errorf("id is required")
	}
	if dc.Schedule == "" {
		return fmt.Errorf("schedule is required")
	}
	// Note: download cleaners don't need arr clients (they work on completed torrents)
	if dc.Trackers.FilterMode != "" && dc.Trackers.FilterMode != "include" && dc.Trackers.FilterMode != "exclude" {
		return fmt.Errorf("trackers.filter_mode must be 'include' or 'exclude'")
	}

	// Validate max_ratio and max_seeding_time
	if dc.MaxRatio < 0 {
		return fmt.Errorf("max_ratio must be >= 0")
	}
	if dc.MaxSeedingTime.Duration() < 0 {
		return fmt.Errorf("max_seeding_time must be >= 0")
	}
	// At least one max setting should be provided
	if dc.MaxRatio == 0 && dc.MaxSeedingTime.Duration() == 0 {
		return fmt.Errorf("at least one of max_ratio or max_seeding_time must be set")
	}

	// Validate bittorrent client IDs
	if len(dc.Clients.Bittorrent) > 0 {
		for _, id := range dc.Clients.Bittorrent {
			if !bittorrentClientIDs[id] {
				return fmt.Errorf("bittorrent client id '%s' does not exist or is not enabled", id)
			}
		}
	}

	// Note: Download cleaners don't use arr clients (they work on completed torrents)
	// The arr field in Clients is ignored for download cleaners

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

// GetArrByID returns an *arr app config by ID and kind, or the first enabled one of that kind if ID is empty
func (c *Config) GetArrByID(id string, kind string) *ArrConfig {
	if id == "" {
		// Return first enabled *arr app of the specified kind
		for i := range c.Arrs {
			if c.Arrs[i].Enabled && c.Arrs[i].Kind == kind {
				return &c.Arrs[i]
			}
		}
		return nil
	}
	for i := range c.Arrs {
		if c.Arrs[i].ID == id && c.Arrs[i].Kind == kind && c.Arrs[i].Enabled {
			return &c.Arrs[i]
		}
	}
	return nil
}

// GetBittorrentClientByID returns a bittorrent client config by ID and kind, or the first enabled one of that kind if ID is empty
func (c *Config) GetBittorrentClientByID(id string, kind string) *BittorrentClientConfig {
	if id == "" {
		// Return first enabled bittorrent client of the specified kind
		for i := range c.BittorrentClients {
			if c.BittorrentClients[i].Enabled && c.BittorrentClients[i].Kind == kind {
				return &c.BittorrentClients[i]
			}
		}
		return nil
	}
	for i := range c.BittorrentClients {
		if c.BittorrentClients[i].ID == id && c.BittorrentClients[i].Kind == kind && c.BittorrentClients[i].Enabled {
			return &c.BittorrentClients[i]
		}
	}
	return nil
}

// GetSonarrByID returns a Sonarr config by ID (deprecated, use GetArrByID with kind="sonarr")
func (c *Config) GetSonarrByID(id string) *SonarrConfig {
	arr := c.GetArrByID(id, "sonarr")
	if arr == nil {
		return nil
	}
	// Convert ArrConfig to SonarrConfig for backward compatibility
	return &SonarrConfig{
		ID:      arr.ID,
		Name:    arr.Name,
		URL:     arr.URL,
		APIKey:  arr.APIKey,
		Enabled: arr.Enabled,
	}
}

// GetQBittorrentByID returns a qBittorrent config by ID (deprecated, use GetBittorrentClientByID with kind="qbittorrent")
func (c *Config) GetQBittorrentByID(id string) *QBittorrentConfig {
	client := c.GetBittorrentClientByID(id, "qbittorrent")
	if client == nil {
		return nil
	}
	// Convert BittorrentClientConfig to QBittorrentConfig for backward compatibility
	return &QBittorrentConfig{
		ID:       client.ID,
		Name:     client.Name,
		URL:      client.URL,
		Username: client.Username,
		Password: client.Password,
		Enabled:  client.Enabled,
	}
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

// Validate checks that a tracker config is valid
func (t *TrackerConfig) Validate() error {
	// MinRatio and MinSeedingTime are optional - if set, they must be valid
	if t.MinRatio < 0 {
		return fmt.Errorf("min_ratio must be >= 0")
	}
	if t.MinSeedingTime.Duration() < 0 {
		return fmt.Errorf("min_seeding_time must be >= 0")
	}
	return nil
}
