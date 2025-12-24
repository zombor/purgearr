package download

import (
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
)

// Cleaner handles download cleaning operations
type Cleaner struct {
	config      config.DownloadCleanerConfig
	qbtClient   *qbittorrent.Client
	sonarrClient *sonarr.Client
	trackers    []config.TrackerConfig // Tracker configurations for filtering
	logger      *slog.Logger
}

// NewCleaner creates a new download cleaner
func NewCleaner(cfg config.DownloadCleanerConfig, qbtClient *qbittorrent.Client, sonarrClient *sonarr.Client, trackers []config.TrackerConfig, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		config:      cfg,
		qbtClient:   qbtClient,
		sonarrClient: sonarrClient,
		trackers:    trackers,
		logger:      logger,
	}
}

// CleanResult represents the result of a cleaning operation
type CleanResult struct {
	DryRun       bool     `json:"dry_run"`
	RemovedCount int      `json:"removed_count"`
	RemovedItems []string `json:"removed_items"`
	Errors       []string `json:"errors"`
}

// Clean performs the download cleaning operation
func (c *Cleaner) Clean() (*CleanResult, error) {
	if !c.config.Enabled {
		return &CleanResult{}, nil
	}

	result := &CleanResult{
		DryRun:       c.config.DryRun,
		RemovedItems: []string{},
		Errors:       []string{},
	}

	// Get torrents from qBittorrent
	torrents, err := c.qbtClient.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("getting torrents: %w", err)
	}

	hashesToDelete := []string{}

	for _, torrent := range torrents {
		// Only process completed torrents
		if torrent.Progress < 1.0 {
			continue
		}

		// Check tracker filter
		if !c.matchesTrackerFilter(torrent.Tracker) {
			continue
		}

		shouldRemove := false
		reason := ""

		// Check max ratio
		if c.config.MaxRatio > 0 && torrent.Ratio >= c.config.MaxRatio {
			shouldRemove = true
			reason = fmt.Sprintf("max_ratio (%.2f)", torrent.Ratio)
		}

		// Check min seeding time
		minSeedingTime := c.config.MinSeedingTime.Duration()
		maxSeedingTime := c.config.MaxSeedingTime.Duration()
		if minSeedingTime > 0 && !shouldRemove {
			seedingDuration := time.Duration(torrent.SeedingTime) * time.Second
			if seedingDuration >= minSeedingTime {
				// Check if max seeding time is also set
				if maxSeedingTime > 0 {
					if seedingDuration >= maxSeedingTime {
						shouldRemove = true
						reason = fmt.Sprintf("max_seeding_time (%s)", seedingDuration)
					}
				} else if c.config.MaxRatio == 0 {
					// Only remove if max ratio is not set, otherwise let ratio take precedence
					shouldRemove = true
					reason = fmt.Sprintf("min_seeding_time (%s)", seedingDuration)
				}
			}
		}

		// Check max seeding time (if min is not set or already met)
		if maxSeedingTime > 0 && !shouldRemove {
			seedingDuration := time.Duration(torrent.SeedingTime) * time.Second
			if seedingDuration >= maxSeedingTime {
				shouldRemove = true
				reason = fmt.Sprintf("max_seeding_time (%s)", seedingDuration)
			}
		}

		if shouldRemove {
			hashesToDelete = append(hashesToDelete, torrent.Hash)
			c.logger.Info("Removing torrent", "cleaner", c.config.Name, "torrent", torrent.Name, "reason", reason)
			result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (%s)", torrent.Name, reason))
		}
	}

	// Delete torrents from qBittorrent
	if len(hashesToDelete) > 0 {
		if c.config.DryRun {
			c.logger.Info("DRY RUN - Would delete torrents", "cleaner", c.config.Name, "count", len(hashesToDelete))
			result.RemovedCount = len(hashesToDelete)
		} else {
			if err := c.qbtClient.DeleteTorrent(hashesToDelete, false); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to delete torrents: %v", err))
				c.logger.Error("Error deleting torrents", "cleaner", c.config.Name, "error", err)
			} else {
				result.RemovedCount = len(hashesToDelete)
			}
		}
	}

	return result, nil
}

// matchesTrackerFilter checks if a tracker URL matches the configured tracker filters
func (c *Cleaner) matchesTrackerFilter(trackerURL string) bool {
	// If no trackers configured, match all
	if len(c.trackers) == 0 || len(c.config.Trackers.TrackerIDs) == 0 {
		return true
	}

	// Check if tracker URL matches any of the configured tracker regex patterns
	matches := false
	for _, tracker := range c.trackers {
		matched, err := regexp.MatchString(tracker.URLRegex, trackerURL)
		if err != nil {
			c.logger.Warn("Invalid regex pattern for tracker", "tracker_id", tracker.ID, "pattern", tracker.URLRegex, "error", err)
			continue
		}
		if matched {
			matches = true
			break
		}
	}

	if c.config.Trackers.FilterMode == "include" {
		return matches
	}
	// exclude mode
	return !matches
}

