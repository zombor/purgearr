package download

import (
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/config"
)

// Cleaner handles download cleaning operations
type Cleaner struct {
	config    config.DownloadCleanerConfig
	qbtClient *qbittorrent.Client
	trackers  []config.TrackerConfig // Tracker configurations for filtering
	logger    *slog.Logger
}

// findTrackerConfig finds the tracker config that matches a tracker URL
func (c *Cleaner) findTrackerConfig(trackerURL string) *config.TrackerConfig {
	for i := range c.trackers {
		tracker := &c.trackers[i]
		matched, err := regexp.MatchString(tracker.URLRegex, trackerURL)
		if err != nil {
			continue
		}
		if matched {
			return tracker
		}
	}
	return nil
}

// NewCleaner creates a new download cleaner
func NewCleaner(cfg config.DownloadCleanerConfig, qbtClient *qbittorrent.Client, trackers []config.TrackerConfig, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		config:    cfg,
		qbtClient: qbtClient,
		trackers:  trackers,
		logger:    logger,
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

	trackerIDs := make([]string, len(c.trackers))
	for i, t := range c.trackers {
		trackerIDs[i] = t.ID
	}
	c.logger.Info("Starting download cleaner run", 
		"cleaner", c.config.Name, 
		"total_torrents", len(torrents), 
		"configured_trackers", trackerIDs, 
		"tracker_filter_mode", c.config.Trackers.FilterMode, 
		"tracker_ids_from_config", c.config.Trackers.TrackerIDs,
		"category_names", c.config.Categories.CategoryNames,
		"category_filter_mode", c.config.Categories.FilterMode)

	hashesToDelete := []string{}
	hashesToPause := []string{} // Torrents to pause when min_progress prevents deletion

	// Build map of hash to torrent for quick lookup
	torrentMap := make(map[string]*qbittorrent.Torrent)
	for i := range torrents {
		torrentMap[torrents[i].Hash] = &torrents[i]
	}

	torrentsProcessed := 0
	torrentsFilteredOut := 0
	for _, torrent := range torrents {
		// Only process completed torrents
		if torrent.Progress < 1.0 {
			continue
		}

		// Check tracker filter
		if !c.matchesTrackerFilter(torrent.Tracker) {
			torrentsFilteredOut++
			continue
		}

		// Check category filter
		if !c.matchesCategoryFilter(torrent.Category) {
			torrentsFilteredOut++
			continue
		}

		torrentsProcessed++

		// Find the tracker config for this torrent
		tracker := c.findTrackerConfig(torrent.Tracker)
		
		// If no tracker config found and filter_mode is exclude, use default values (no min requirements)
		// This allows processing torrents from trackers not in the exclude list
		var minRatio float64 = 0
		var minSeedingTime config.Duration = 0
		if tracker != nil {
			minRatio = tracker.MinRatio
			minSeedingTime = tracker.MinSeedingTime
		} else if c.config.Trackers.FilterMode == "exclude" {
			// Exclude mode: torrents from non-configured trackers can be processed with no min requirements
			c.logger.Debug("No tracker config found, using default min requirements (0)", "torrent", torrent.Name, "tracker_url", torrent.Tracker)
		} else {
			// Include mode: require tracker config
			c.logger.Debug("No tracker config found for torrent", "torrent", torrent.Name, "tracker_url", torrent.Tracker)
			continue
		}

		seedingDuration := time.Duration(torrent.SeedingTime) * time.Second

		// Check if minimum requirements are met (must be met before deletion is allowed)
		minRatioMet := minRatio == 0 || torrent.Ratio >= minRatio
		minSeedingTimeMet := minSeedingTime.Duration() == 0 || seedingDuration >= minSeedingTime.Duration()

		if !minRatioMet || !minSeedingTimeMet {
			// Minimum requirements not met - do not delete
			trackerName := "unknown"
			if tracker != nil {
				trackerName = tracker.Name
			}
			c.logger.Debug("Skipping torrent - minimum requirements not met", 
				"cleaner", c.config.Name, 
				"torrent", torrent.Name, 
				"tracker", trackerName,
				"ratio", torrent.Ratio,
				"min_ratio", minRatio,
				"seeding_time", seedingDuration,
				"min_seeding_time", minSeedingTime.Duration())
			continue
		}

		// Minimum requirements met - check if max limits are reached
		shouldRemove := false
		reason := ""

		// Check max ratio (OR logic - clean if either max is reached)
		if c.config.MaxRatio > 0 && torrent.Ratio >= c.config.MaxRatio {
			shouldRemove = true
			reason = fmt.Sprintf("max_ratio (%.2f >= %.2f)", torrent.Ratio, c.config.MaxRatio)
		}

		// Check max seeding time (OR logic - clean if either max is reached)
		if !shouldRemove && c.config.MaxSeedingTime.Duration() > 0 && seedingDuration >= c.config.MaxSeedingTime.Duration() {
			shouldRemove = true
			reason = fmt.Sprintf("max_seeding_time (%s >= %s)", seedingDuration, c.config.MaxSeedingTime.Duration())
		}

		if shouldRemove {
			hashesToDelete = append(hashesToDelete, torrent.Hash)
			trackerName := "unknown"
			if tracker != nil {
				trackerName = tracker.Name
			}
			c.logger.Info("Removing torrent", "cleaner", c.config.Name, "torrent", torrent.Name, "tracker", trackerName, "reason", reason)
			result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (%s)", torrent.Name, reason))
		}
	}

	// Filter out torrents that haven't reached their tracker's min_progress threshold
	// If min_progress prevents deletion, pause instead
	filteredHashesToDelete := []string{}
	for _, hash := range hashesToDelete {
		torrent, exists := torrentMap[hash]
		if !exists {
			continue
		}

		trackerConfig := c.findTrackerConfig(torrent.Tracker)
		if trackerConfig != nil && trackerConfig.MinProgress > 0 {
			// Check if torrent progress is below the tracker's min_progress threshold
			if torrent.Progress < trackerConfig.MinProgress {
				// Pause instead of deleting
				hashesToPause = append(hashesToPause, hash)
				c.logger.Info("Pausing torrent instead of deleting due to tracker min_progress", 
					"cleaner", c.config.Name, 
					"torrent", torrent.Name, 
					"progress", torrent.Progress, 
					"min_progress", trackerConfig.MinProgress,
					"tracker", trackerConfig.Name)
				continue
			}
		}

		filteredHashesToDelete = append(filteredHashesToDelete, hash)
	}

	// Delete torrents from qBittorrent
	if len(filteredHashesToDelete) > 0 {
		if c.config.DryRun {
			deleteAction := "torrents"
			if c.config.DeleteFiles {
				deleteAction = "torrents and files"
			}
			c.logger.Info("DRY RUN - Would delete "+deleteAction, "cleaner", c.config.Name, "count", len(filteredHashesToDelete))
			result.RemovedCount = len(filteredHashesToDelete)
		} else {
			if err := c.qbtClient.DeleteTorrent(filteredHashesToDelete, c.config.DeleteFiles); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to delete torrents: %v", err))
				c.logger.Error("Error deleting torrents", "cleaner", c.config.Name, "error", err)
			} else {
				deleteAction := "torrents"
				if c.config.DeleteFiles {
					deleteAction = "torrents and files"
				}
				c.logger.Info("Deleted "+deleteAction, "cleaner", c.config.Name, "count", len(filteredHashesToDelete))
				result.RemovedCount = len(filteredHashesToDelete)
			}
		}
	}

	// Pause torrents when min_progress prevents deletion
	if len(hashesToPause) > 0 {
		if c.config.DryRun {
			c.logger.Info("DRY RUN - Would pause torrents", "cleaner", c.config.Name, "count", len(hashesToPause))
		} else {
			if err := c.qbtClient.PauseTorrent(hashesToPause); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to pause torrents: %v", err))
				c.logger.Error("Error pausing torrents", "cleaner", c.config.Name, "error", err)
			} else {
				c.logger.Info("Paused torrents", "cleaner", c.config.Name, "count", len(hashesToPause))
			}
		}
	}

	c.logger.Info("Finished processing torrents", "cleaner", c.config.Name, "total_torrents", len(torrents), "torrents_filtered_out", torrentsFilteredOut, "torrents_processed", torrentsProcessed, "torrents_to_delete", len(filteredHashesToDelete), "torrents_to_pause", len(hashesToPause))

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

// matchesCategoryFilter checks if a category matches the configured category filters
func (c *Cleaner) matchesCategoryFilter(category string) bool {
	// If no categories configured, match all
	if len(c.config.Categories.CategoryNames) == 0 {
		return true
	}

	// Check if category matches any of the configured category names
	matches := false
	for _, categoryName := range c.config.Categories.CategoryNames {
		if category == categoryName {
			matches = true
			break
		}
	}

	if c.config.Categories.FilterMode == "include" {
		return matches
	}
	// exclude mode
	return !matches
}

