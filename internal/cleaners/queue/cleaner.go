package queue

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
)

// arrQueueItem represents a queue item from an *arr app
type arrQueueItem struct {
	arrID string
	item  *sonarr.QueueItem
}

// ArrClient represents a generic *arr app client interface
type ArrClient interface {
	GetQueue() (*sonarr.QueueResponse, error)
	RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error
}

// torrentStrikes tracks strikes for each torrent hash
type torrentStrikes struct {
	stalledStrikes      int
	slowStrikes         int
	failedImportStrikes int
	lastProgress        float64 // Last known progress percentage
	lastCheckTime       time.Time
}

// Cleaner handles queue cleaning operations
type Cleaner struct {
	config     config.QueueCleanerConfig
	qbtClient  *qbittorrent.Client
	arrClients map[string]ArrClient   // Map of arr app ID to client
	trackers   []config.TrackerConfig // Tracker configurations for filtering
	logger     *slog.Logger
	strikes    map[string]*torrentStrikes // Map of torrent hash to strikes
	strikesMu  sync.RWMutex               // Mutex for thread-safe access to strikes map
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

// isArrConfigured checks if an arr app ID is configured for this cleaner
func (c *Cleaner) isArrConfigured(arrID string) bool {
	// If no arrs configured, use all enabled arr apps
	if len(c.config.Arrs) == 0 {
		return true
	}
	// Check if this arr ID is in the configured list
	for _, id := range c.config.Arrs {
		if id == arrID {
			return true
		}
	}
	return false
}

// NewCleaner creates a new queue cleaner
func NewCleaner(cfg config.QueueCleanerConfig, qbtClient *qbittorrent.Client, arrClients map[string]ArrClient, trackers []config.TrackerConfig, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		config:     cfg,
		qbtClient:  qbtClient,
		arrClients: arrClients,
		trackers:   trackers,
		logger:     logger,
		strikes:    make(map[string]*torrentStrikes),
	}
}

// CleanResult represents the result of a cleaning operation
type CleanResult struct {
	DryRun       bool     `json:"dry_run"`
	RemovedCount int      `json:"removed_count"`
	RemovedItems []string `json:"removed_items"`
	Errors       []string `json:"errors"`
}

// getOrCreateStrikes gets or creates strike tracking for a torrent hash
func (c *Cleaner) getOrCreateStrikes(hash string) *torrentStrikes {
	c.strikesMu.Lock()
	defer c.strikesMu.Unlock()

	if strikes, ok := c.strikes[hash]; ok {
		return strikes
	}

	strikes := &torrentStrikes{
		lastCheckTime: time.Now(),
	}
	c.strikes[hash] = strikes
	return strikes
}

// isImportFailed checks if a Sonarr queue item indicates import failure
func isImportFailed(item *sonarr.QueueItem) bool {
	// Check Status field - common failure statuses in Sonarr
	if item.Status == "warning" || item.Status == "error" {
		return true
	}

	// Check ErrorMessage field
	if item.ErrorMessage != "" {
		return true
	}

	// Check StatusMessages for error indicators
	for _, statusMsg := range item.StatusMessages {
		if statusMsg.Title == "Import failed" || statusMsg.Title == "Download failed" {
			return true
		}
		for _, msg := range statusMsg.Messages {
			if contains(msg, "import", "failed", "error") {
				return true
			}
		}
	}

	return false
}

// contains checks if any of the search terms appear in the text (case-insensitive)
func contains(text string, terms ...string) bool {
	lowerText := strings.ToLower(text)
	for _, term := range terms {
		if strings.Contains(lowerText, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

// Clean performs the queue cleaning operation
func (c *Cleaner) Clean() (*CleanResult, error) {
	if !c.config.Enabled {
		return &CleanResult{}, nil
	}

	result := &CleanResult{
		DryRun:       c.config.DryRun,
		RemovedItems: []string{},
		Errors:       []string{},
	}

	// Get queues from all configured *arr apps (source of truth)
	// Map of torrent hash to queue items (hash -> []*arrQueueItem)
	arrQueueMap := make(map[string][]arrQueueItem)
	totalQueueItems := 0

	for arrID, arrClient := range c.arrClients {
		// Only process arr apps that are configured for this cleaner
		if !c.isArrConfigured(arrID) {
			continue
		}

		queue, err := arrClient.GetQueue()
		if err != nil {
			c.logger.Warn("Failed to get queue from arr app", "arr_id", arrID, "error", err)
			continue
		}

		// Build map of download IDs (hashes) to queue items
		for i := range queue.Records {
			item := &queue.Records[i]
			if item.DownloadID != "" {
				// DownloadID in Sonarr is typically the torrent hash
				hash := item.DownloadID
				arrQueueMap[hash] = append(arrQueueMap[hash], arrQueueItem{
					arrID: arrID,
					item:  item,
				})
				totalQueueItems++
			}
		}
	}

	if len(arrQueueMap) == 0 {
		c.logger.Info("No queue items found in arr apps", "cleaner", c.config.Name)
		return result, nil
	}

	// Get torrents from qBittorrent to check their status
	torrents, err := c.qbtClient.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("getting torrents: %w", err)
	}

	// Build map of hash to torrent for quick lookup
	torrentMap := make(map[string]*qbittorrent.Torrent)
	for i := range torrents {
		torrentMap[torrents[i].Hash] = &torrents[i]
	}

	trackerIDs := make([]string, len(c.trackers))
	for i, t := range c.trackers {
		trackerIDs[i] = t.ID
	}
	c.logger.Info("Starting queue cleaner run", "cleaner", c.config.Name, "total_queue_items", totalQueueItems, "configured_trackers", trackerIDs, "filter_mode", c.config.Trackers.FilterMode, "tracker_ids_from_config", c.config.Trackers.TrackerIDs)

	// Track which torrents should be removed from *arr apps
	hashesToRemoveFromArr := make(map[string]bool)

	queueItemsProcessed := 0
	queueItemsFilteredOut := 0
	sampleFilteredTrackers := make(map[string]int) // Track sample tracker URLs that were filtered out
	stalledStatesSeen := make(map[string]int)      // Track what states we see for debugging

	// Iterate over queue items from *arr apps (source of truth)
	for hash, queueItems := range arrQueueMap {
		// Find the corresponding torrent in qBittorrent
		torrent, exists := torrentMap[hash]
		if !exists {
			c.logger.Debug("Queue item has no corresponding torrent in bittorrent client", "hash", hash, "title", queueItems[0].item.Title)
			queueItemsFilteredOut++
			continue
		}

		// Only process torrents that are downloading or stalled (not completed/seeding)
		// Queue cleaner should only act on active downloads, not completed ones
		if torrent.Progress >= 1.0 {
			// Torrent is completed, skip it
			c.logger.Debug("Skipping completed torrent", "torrent", torrent.Name, "state", torrent.State, "progress", torrent.Progress)
			queueItemsFilteredOut++
			continue
		}

		// Collect sample states for debugging
		stalledStatesSeen[torrent.State]++

		// Check tracker filter
		if !c.matchesTrackerFilter(torrent.Tracker) {
			queueItemsFilteredOut++

			// Special logging for stalled torrents that are being filtered out
			if torrent.State == "stalledDL" || torrent.State == "stalledUP" || strings.HasPrefix(strings.ToLower(torrent.State), "stalled") {
				c.logger.Info("Stalled torrent filtered out by tracker", "torrent", torrent.Name, "state", torrent.State, "tracker_url", torrent.Tracker, "filter_mode", c.config.Trackers.FilterMode, "configured_trackers", c.config.Trackers.TrackerIDs)
			}

			// Collect sample tracker URLs for logging
			if len(sampleFilteredTrackers) < 5 {
				sampleFilteredTrackers[torrent.Tracker]++
			}
			continue
		}
		queueItemsProcessed++

		// Get or create strike tracking for this torrent
		strikes := c.getOrCreateStrikes(torrent.Hash)

		// Check for stalled status
		if c.config.Stalled != nil {
			// qBittorrent states: stalledDL, stalledUP, downloading, uploading, etc.
			// Also check for "Stalled" status (capital S) which might be used in some versions
			isStalled := torrent.State == "stalledDL" || torrent.State == "stalledUP" || torrent.State == "stalled" || strings.HasPrefix(strings.ToLower(torrent.State), "stalled")

			// Log all stalled torrents that pass the tracker filter
			if isStalled {
				c.logger.Info("Processing stalled torrent", "torrent", torrent.Name, "state", torrent.State, "tracker_url", torrent.Tracker, "progress", torrent.Progress)
			}

			if isStalled {
				// Check if progress was made (reset strikes if reset_on_progress is enabled)
				if c.config.Stalled.ResetOnProgress && torrent.Progress > strikes.lastProgress {
					c.strikesMu.Lock()
					oldStrikes := strikes.stalledStrikes
					strikes.stalledStrikes = 0
					strikes.lastProgress = torrent.Progress
					c.strikesMu.Unlock()
					c.logger.Info("Reset stalled strikes due to progress", "cleaner", c.config.Name, "torrent", torrent.Name, "old_strikes", oldStrikes, "progress", torrent.Progress, "last_progress", strikes.lastProgress)
				} else {
					c.strikesMu.Lock()
					strikes.stalledStrikes++
					currentStrikes := strikes.stalledStrikes
					lastProgress := strikes.lastProgress
					c.strikesMu.Unlock()
					c.logger.Info("Incremented stalled strikes", "cleaner", c.config.Name, "torrent", torrent.Name, "state", torrent.State, "strikes", currentStrikes, "threshold", c.config.Stalled.Strikes, "progress", torrent.Progress, "last_progress", lastProgress)
				}
			} else {
				// Not stalled, reset strikes
				c.strikesMu.Lock()
				strikes.stalledStrikes = 0
				c.strikesMu.Unlock()
			}

			// Update last progress
			c.strikesMu.Lock()
			strikes.lastProgress = torrent.Progress
			c.strikesMu.Unlock()

			if strikes.stalledStrikes >= c.config.Stalled.Strikes {
				hashesToRemoveFromArr[hash] = true
				c.logger.Info("Torrent marked for removal from arr (stalled)", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", strikes.stalledStrikes)
				result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (stalled, %d strikes)", torrent.Name, strikes.stalledStrikes))
			}
		}

		// Check for slow download
		if c.config.Slow != nil && torrent.State == "downloading" && c.config.Slow.MinSpeed.Bytes() > 0 {
			isSlow := torrent.Dlspeed < c.config.Slow.MinSpeed.Bytes()

			if isSlow {
				// Check if progress was made (reset strikes if reset_on_progress is enabled)
				if c.config.Slow.ResetOnProgress && torrent.Progress > strikes.lastProgress {
					c.strikesMu.Lock()
					strikes.slowStrikes = 0
					strikes.lastProgress = torrent.Progress
					c.strikesMu.Unlock()
					c.logger.Debug("Reset slow strikes due to progress", "torrent", torrent.Name, "progress", torrent.Progress)
				} else {
					c.strikesMu.Lock()
					strikes.slowStrikes++
					currentStrikes := strikes.slowStrikes
					c.strikesMu.Unlock()
					c.logger.Info("Incremented slow strikes", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", currentStrikes, "threshold", c.config.Slow.Strikes, "speed", torrent.Dlspeed, "min_speed", c.config.Slow.MinSpeed.Bytes())
				}
			} else {
				// Not slow, reset strikes
				c.strikesMu.Lock()
				strikes.slowStrikes = 0
				c.strikesMu.Unlock()
			}

			// Update last progress
			c.strikesMu.Lock()
			strikes.lastProgress = torrent.Progress
			c.strikesMu.Unlock()

			if strikes.slowStrikes >= c.config.Slow.Strikes {
				hashesToRemoveFromArr[hash] = true
				c.logger.Info("Torrent marked for removal from arr (slow)", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", strikes.slowStrikes, "speed", torrent.Dlspeed)
				result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (slow, %d strikes, %d bytes/s)", torrent.Name, strikes.slowStrikes, torrent.Dlspeed))
			}
		}

		// Check for failed import in *arr apps (queueItems is already available from the loop)
		if c.config.FailedImport != nil {
			hasFailedImport := false
			for _, qItem := range queueItems {
				if isImportFailed(qItem.item) {
					hasFailedImport = true
					break
				}
			}

			if hasFailedImport {
				c.strikesMu.Lock()
				strikes.failedImportStrikes++
				currentStrikes := strikes.failedImportStrikes
				c.strikesMu.Unlock()
				c.logger.Info("Incremented failed import strikes", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", currentStrikes, "threshold", c.config.FailedImport.Strikes)
			} else {
				// Import not failed, reset strikes
				c.strikesMu.Lock()
				strikes.failedImportStrikes = 0
				c.strikesMu.Unlock()
			}

			if strikes.failedImportStrikes >= c.config.FailedImport.Strikes {
				hashesToRemoveFromArr[hash] = true
				c.logger.Info("Torrent marked for removal from arr (failed import)", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", strikes.failedImportStrikes)
				result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (failed import, %d strikes)", torrent.Name, strikes.failedImportStrikes))
			}
		}
	}

	c.logger.Info("Finished processing queue items", "cleaner", c.config.Name, "total_queue_items", totalQueueItems, "queue_items_filtered_out", queueItemsFilteredOut, "queue_items_processed", queueItemsProcessed, "queue_items_to_remove_from_arr", len(hashesToRemoveFromArr))
	if len(stalledStatesSeen) > 0 {
		c.logger.Info("Sample torrent states seen", "cleaner", c.config.Name, "states", stalledStatesSeen)
	}
	if queueItemsFilteredOut > 0 && len(sampleFilteredTrackers) > 0 {
		c.logger.Info("Sample tracker URLs that were filtered out", "cleaner", c.config.Name, "sample_trackers", sampleFilteredTrackers, "filter_mode", c.config.Trackers.FilterMode, "configured_tracker_ids", c.config.Trackers.TrackerIDs)
	}

	// Clean up strikes for queue items that no longer exist
	c.strikesMu.Lock()
	for hash := range c.strikes {
		if _, exists := arrQueueMap[hash]; !exists {
			delete(c.strikes, hash)
		}
	}
	c.strikesMu.Unlock()

	// Remove from *arr apps (only action for queue cleaner - never touches bittorrent client)
	arrRemovedCount := 0
	for hash := range hashesToRemoveFromArr {
		if queueItems, ok := arrQueueMap[hash]; ok {
			for _, qItem := range queueItems {
				// Only remove from arr apps that are configured for this cleaner
				if !c.isArrConfigured(qItem.arrID) {
					continue
				}

				if c.config.DryRun {
					c.logger.Info("DRY RUN - Would remove item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID)
				} else {
					arrClient := c.arrClients[qItem.arrID]
					if arrClient != nil {
						// Queue cleaner never removes from bittorrent client - only from arr app
						if err := arrClient.RemoveFromQueue(qItem.item.ID, false, false); err != nil {
							c.logger.Warn("Failed to remove item from arr queue", "cleaner", c.config.Name, "item_id", qItem.item.ID, "arr_id", qItem.arrID, "error", err)
							result.Errors = append(result.Errors, fmt.Sprintf("failed to remove from %s queue: %v", qItem.arrID, err))
						} else {
							arrRemovedCount++
							c.logger.Info("Removed item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID)
						}
					}
				}
			}
		}
	}
	result.RemovedCount = arrRemovedCount

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
		// Compile regex once for case-insensitive matching
		fullPattern := "(?i)" + tracker.URLRegex
		re, err := regexp.Compile(fullPattern)
		if err != nil {
			c.logger.Warn("Invalid regex pattern for tracker", "tracker_id", tracker.ID, "pattern", tracker.URLRegex, "full_pattern", fullPattern, "error", err)
			continue
		}
		matched := re.MatchString(trackerURL)
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
