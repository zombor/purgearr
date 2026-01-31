package download

import (
	"fmt"
	"log/slog"
	"regexp"
	"sort"
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

		// Check if minimum requirements are met (OR logic - either min ratio OR min seeding time must be met)
		minRatioMet := minRatio == 0 || torrent.Ratio >= minRatio
		minSeedingTimeMet := minSeedingTime.Duration() == 0 || seedingDuration >= minSeedingTime.Duration()

		// At least one minimum requirement must be met (OR logic)
		if !minRatioMet && !minSeedingTimeMet {
			// Neither minimum requirement met - do not delete
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

// CandidateTorrent represents a torrent that is a candidate for cleaning
type CandidateTorrent struct {
	Hash                string  `json:"hash"`
	Name                string  `json:"name"`
	Ratio               float64 `json:"ratio"`
	SeedingTime         int64   `json:"seeding_time"` // in seconds
	Tracker             string  `json:"tracker"`
	TrackerName         string  `json:"tracker_name"`
	TrackerMinRatio     float64 `json:"tracker_min_ratio"`
	CleanerMaxRatio     float64 `json:"cleaner_max_ratio"`
	CleanerMaxSeeding   int64   `json:"cleaner_max_seeding"`    // in seconds
	TimeUntilMaxRatio   int64   `json:"time_until_max_ratio"`   // in seconds, -1 if already reached or not applicable
	TimeUntilMaxSeeding int64   `json:"time_until_max_seeding"` // in seconds, -1 if already reached or not applicable
	TimeUntilClean      int64   `json:"time_until_clean"`       // in seconds, negative if already eligible
}

// GetCandidateTorrents returns torrents that are candidates for cleaning, sorted by closest to being cleaned
func (c *Cleaner) GetCandidateTorrents() ([]CandidateTorrent, error) {
	// Get torrents from qBittorrent
	torrents, err := c.qbtClient.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("getting torrents: %w", err)
	}

	candidates := []CandidateTorrent{}

	for _, torrent := range torrents {
		// Only process completed torrents
		if torrent.Progress < 1.0 {
			continue
		}

		// Check tracker filter
		if !c.matchesTrackerFilter(torrent.Tracker) {
			continue
		}

		// Check category filter
		if !c.matchesCategoryFilter(torrent.Category) {
			continue
		}

		// Find the tracker config for this torrent
		tracker := c.findTrackerConfig(torrent.Tracker)

		var trackerName string = "unknown"
		var trackerMinRatio float64 = 0
		if tracker != nil {
			trackerName = tracker.Name
			trackerMinRatio = tracker.MinRatio
		}

		seedingDuration := time.Duration(torrent.SeedingTime) * time.Second
		maxSeedingDuration := c.config.MaxSeedingTime.Duration()

		// Calculate time until max ratio and max seeding separately
		// Use 0 to mean "not applicable" (max limit not set), -1 means "already reached", positive means "time remaining"
		var timeUntilMaxRatio int64 = 0   // 0 = not applicable, -1 = reached, >0 = seconds until reached
		var timeUntilMaxSeeding int64 = 0 // 0 = not applicable, -1 = reached, >0 = seconds until reached
		var timeUntilClean int64 = 0

		// Always calculate max times (regardless of min requirements) for display purposes

		// Calculate time until max ratio (if max ratio is set)
		if c.config.MaxRatio > 0 {
			if torrent.Ratio >= c.config.MaxRatio {
				timeUntilMaxRatio = -1 // Already reached
			} else {
				// Estimate time based on current upload speed
				if torrent.Upspeed > 0 && torrent.Downloaded > 0 {
					// Calculate how much more upload is needed
					// ratio = uploaded / downloaded
					// max_ratio = (uploaded + additional) / downloaded
					// additional = (max_ratio * downloaded) - uploaded
					additionalUpload := (c.config.MaxRatio * float64(torrent.Downloaded)) - float64(torrent.Uploaded)
					if additionalUpload > 0 {
						timeUntilMaxRatio = int64(additionalUpload / float64(torrent.Upspeed))
					} else {
						// This shouldn't happen if ratio < maxRatio, but handle it
						timeUntilMaxRatio = -1 // Already reached
					}
				} else {
					// Can't estimate - no upload speed or no downloaded data
					timeUntilMaxRatio = 999999999 // Very large number (can't estimate)
				}
			}
		}
		// else: timeUntilMaxRatio stays 0 (not applicable)

		// Calculate time until max seeding time
		if maxSeedingDuration > 0 {
			if seedingDuration >= maxSeedingDuration {
				timeUntilMaxSeeding = -1 // Already reached
			} else {
				timeUntilMaxSeeding = int64((maxSeedingDuration - seedingDuration).Seconds())
			}
		}
		// else: timeUntilMaxSeeding stays 0 (not applicable)

		// Check if minimum requirements are met
		minRatioMet := trackerMinRatio == 0 || torrent.Ratio >= trackerMinRatio
		minSeedingTimeMet := tracker == nil || tracker.MinSeedingTime.Duration() == 0 || seedingDuration >= tracker.MinSeedingTime.Duration()

		if minRatioMet && minSeedingTimeMet {
			// Min requirements met - check if max limits are reached (OR logic - either one makes it eligible)
			maxRatioReached := c.config.MaxRatio > 0 && torrent.Ratio >= c.config.MaxRatio
			maxSeedingReached := maxSeedingDuration > 0 && seedingDuration >= maxSeedingDuration

			if maxRatioReached || maxSeedingReached {
				// Already eligible for cleaning
				timeUntilClean = -1
			} else {
				// Time until clean is the minimum of the two (whichever limit is reached first)
				// Only consider valid times (not -1 or 999999999)
				validRatioTime := timeUntilMaxRatio > 0 && timeUntilMaxRatio < 999999999
				validSeedingTime := timeUntilMaxSeeding > 0

				if validRatioTime && validSeedingTime {
					if timeUntilMaxRatio < timeUntilMaxSeeding {
						timeUntilClean = timeUntilMaxRatio
					} else {
						timeUntilClean = timeUntilMaxSeeding
					}
				} else if validRatioTime {
					timeUntilClean = timeUntilMaxRatio
				} else if validSeedingTime {
					timeUntilClean = timeUntilMaxSeeding
				} else {
					timeUntilClean = -1 // Can't determine
				}
			}
		} else {
			// Min requirements not met - calculate time until min requirements are met
			var timeUntilMinRatio int64 = 0
			var timeUntilMinSeeding int64 = 0
			var canMeetMinRatio bool = true

			// Time until min ratio
			if trackerMinRatio > 0 && torrent.Ratio < trackerMinRatio {
				if torrent.Upspeed > 0 && torrent.Downloaded > 0 {
					additionalUpload := (trackerMinRatio * float64(torrent.Downloaded)) - float64(torrent.Uploaded)
					if additionalUpload > 0 {
						timeUntilMinRatio = int64(additionalUpload / float64(torrent.Upspeed))
					} else {
						timeUntilMinRatio = 0 // Already met
					}
				} else {
					// Can't meet min ratio (no upload speed)
					canMeetMinRatio = false
					timeUntilMinRatio = 999999999
				}
			}

			// Time until min seeding time
			if tracker != nil && tracker.MinSeedingTime.Duration() > 0 && seedingDuration < tracker.MinSeedingTime.Duration() {
				timeUntilMinSeeding = int64((tracker.MinSeedingTime.Duration() - seedingDuration).Seconds())
			}

			// Time until min requirements are met (whichever is longer)
			timeUntilMinRequirements := timeUntilMinRatio
			if timeUntilMinSeeding > timeUntilMinRatio {
				timeUntilMinRequirements = timeUntilMinSeeding
			}

			// Now calculate time until clean
			// The torrent will be cleaned when: min requirements are met AND max limits are reached
			// Since max limits are calculated from NOW, we need to find when both conditions will be true

			validRatioTime := timeUntilMaxRatio > 0 && timeUntilMaxRatio < 999999999
			validSeedingTime := timeUntilMaxSeeding > 0

			// If we can't meet min ratio, we can only clean via seeding time path
			if !canMeetMinRatio && trackerMinRatio > 0 {
				// Can only clean via seeding time: need min seeding time AND max seeding time
				// Max seeding time is calculated from now, so we need max(min seeding time, max seeding time from now)
				// But actually, we need BOTH: min seeding time must be met, AND then max seeding time must be reached
				// So: time until min seeding + remaining time until max seeding (from when min is met)
				if validSeedingTime {
					// If max seeding time is reached before min seeding time, we still need to wait for min
					if timeUntilMaxSeeding < timeUntilMinSeeding {
						timeUntilClean = timeUntilMinSeeding
					} else {
						// Min seeding will be met first, then we need remaining time until max seeding
						// But max seeding is calculated from now, so: max seeding time from now
						timeUntilClean = timeUntilMaxSeeding
					}
				} else if timeUntilMaxSeeding == -1 {
					// Max seeding already reached, but min seeding not met yet
					timeUntilClean = timeUntilMinSeeding
				} else {
					timeUntilClean = 999999999 // Can't determine
				}
			} else {
				// Can meet both min requirements - consider both ratio and seeding paths
				// For each path, we need: max(min requirements) AND max limit
				// Since max limits are from now, we take: max(time until min requirements, time until max limit)
				// Then take the minimum of both paths

				var ratioPathTime int64 = 999999999
				var seedingPathTime int64 = 999999999

				// Ratio path: need min requirements met AND max ratio reached
				if validRatioTime {
					// Time until both are true: max(time until min requirements, time until max ratio)
					if timeUntilMinRequirements > timeUntilMaxRatio {
						ratioPathTime = timeUntilMinRequirements
					} else {
						ratioPathTime = timeUntilMaxRatio
					}
				} else if timeUntilMaxRatio == -1 {
					// Max ratio already reached, but min requirements not met yet
					ratioPathTime = timeUntilMinRequirements
				}

				// Seeding path: need min requirements met AND max seeding reached
				if validSeedingTime {
					// Time until both are true: max(time until min requirements, time until max seeding)
					if timeUntilMinRequirements > timeUntilMaxSeeding {
						seedingPathTime = timeUntilMinRequirements
					} else {
						seedingPathTime = timeUntilMaxSeeding
					}
				} else if timeUntilMaxSeeding == -1 {
					// Max seeding already reached, but min requirements not met yet
					seedingPathTime = timeUntilMinRequirements
				}

				// Time until clean is whichever path is shorter (OR logic - either path can trigger cleaning)
				if ratioPathTime < seedingPathTime {
					timeUntilClean = ratioPathTime
				} else {
					timeUntilClean = seedingPathTime
				}

				// If both are invalid, fall back to just time until min requirements
				if timeUntilClean >= 999999999 {
					timeUntilClean = timeUntilMinRequirements
				}
			}
		}

		candidate := CandidateTorrent{
			Hash:                torrent.Hash,
			Name:                torrent.Name,
			Ratio:               torrent.Ratio,
			SeedingTime:         torrent.SeedingTime,
			Tracker:             torrent.Tracker,
			TrackerName:         trackerName,
			TrackerMinRatio:     trackerMinRatio,
			CleanerMaxRatio:     c.config.MaxRatio,
			CleanerMaxSeeding:   int64(maxSeedingDuration.Seconds()),
			TimeUntilMaxRatio:   timeUntilMaxRatio,
			TimeUntilMaxSeeding: timeUntilMaxSeeding,
			TimeUntilClean:      timeUntilClean,
		}

		candidates = append(candidates, candidate)
	}

	// Sort by time until clean (closest first, negative values first)
	sort.Slice(candidates, func(i, j int) bool {
		// Negative values (already eligible) come first
		if candidates[i].TimeUntilClean < 0 && candidates[j].TimeUntilClean >= 0 {
			return true
		}
		if candidates[i].TimeUntilClean >= 0 && candidates[j].TimeUntilClean < 0 {
			return false
		}
		// Both negative or both positive - sort by absolute value (smaller first)
		if candidates[i].TimeUntilClean < 0 && candidates[j].TimeUntilClean < 0 {
			return candidates[i].TimeUntilClean > candidates[j].TimeUntilClean // More negative = more eligible
		}
		return candidates[i].TimeUntilClean < candidates[j].TimeUntilClean
	})

	return candidates, nil
}
