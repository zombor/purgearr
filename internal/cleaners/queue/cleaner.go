package queue

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zombor/purgearr/internal/clients/lidarr"
	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/radarr"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
)

// QueueItem represents a generic queue item from an *arr app
type QueueItem struct {
	ID             int
	Title          string
	Status         string
	StatusMessages []StatusMessage
	ErrorMessage   string
	DownloadID     string
}

// StatusMessage represents a status message for a queue item
type StatusMessage struct {
	Title    string
	Messages []string
}

// QueueResponse represents a generic queue response from an *arr app
type QueueResponse struct {
	Records []QueueItem
}

// arrQueueItem represents a queue item from an *arr app
type arrQueueItem struct {
	arrID string
	item  QueueItem
}

// ArrClient represents a generic *arr app client interface
type ArrClient interface {
	GetQueue() (*QueueResponse, error)
	RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error
}

// SonarrClientWrapper wraps a Sonarr client to implement ArrClient
type SonarrClientWrapper struct {
	Client *sonarr.Client
}

func (w *SonarrClientWrapper) GetQueue() (*QueueResponse, error) {
	resp, err := w.Client.GetQueue()
	if err != nil {
		return nil, err
	}

	records := make([]QueueItem, len(resp.Records))
	for i, item := range resp.Records {
		statusMessages := make([]StatusMessage, len(item.StatusMessages))
		for j, sm := range item.StatusMessages {
			statusMessages[j] = StatusMessage{
				Title:    sm.Title,
				Messages: sm.Messages,
			}
		}
		records[i] = QueueItem{
			ID:             item.ID,
			Title:          item.Title,
			Status:         item.Status,
			StatusMessages: statusMessages,
			ErrorMessage:   item.ErrorMessage,
			DownloadID:     item.DownloadID,
		}
	}

	return &QueueResponse{
		Records: records,
	}, nil
}

func (w *SonarrClientWrapper) RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error {
	return w.Client.RemoveFromQueue(id, removeFromClient, blocklist)
}

// RadarrClientWrapper wraps a Radarr client to implement ArrClient
type RadarrClientWrapper struct {
	Client *radarr.Client
}

func (w *RadarrClientWrapper) GetQueue() (*QueueResponse, error) {
	resp, err := w.Client.GetQueue()
	if err != nil {
		return nil, err
	}

	records := make([]QueueItem, len(resp.Records))
	for i, item := range resp.Records {
		statusMessages := make([]StatusMessage, len(item.StatusMessages))
		for j, sm := range item.StatusMessages {
			statusMessages[j] = StatusMessage{
				Title:    sm.Title,
				Messages: sm.Messages,
			}
		}
		records[i] = QueueItem{
			ID:             item.ID,
			Title:          item.Title,
			Status:         item.Status,
			StatusMessages: statusMessages,
			ErrorMessage:   item.ErrorMessage,
			DownloadID:     item.DownloadID,
		}
	}

	return &QueueResponse{
		Records: records,
	}, nil
}

func (w *RadarrClientWrapper) RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error {
	return w.Client.RemoveFromQueue(id, removeFromClient, blocklist)
}

// LidarrClientWrapper wraps a Lidarr client to implement ArrClient
type LidarrClientWrapper struct {
	Client *lidarr.Client
}

func (w *LidarrClientWrapper) GetQueue() (*QueueResponse, error) {
	resp, err := w.Client.GetQueue()
	if err != nil {
		return nil, err
	}

	records := make([]QueueItem, len(resp.Records))
	for i, item := range resp.Records {
		statusMessages := make([]StatusMessage, len(item.StatusMessages))
		for j, sm := range item.StatusMessages {
			statusMessages[j] = StatusMessage{
				Title:    sm.Title,
				Messages: sm.Messages,
			}
		}
		records[i] = QueueItem{
			ID:             item.ID,
			Title:          item.Title,
			Status:         item.Status,
			StatusMessages: statusMessages,
			ErrorMessage:   item.ErrorMessage,
			DownloadID:     item.DownloadID,
		}
	}

	return &QueueResponse{
		Records: records,
	}, nil
}

func (w *LidarrClientWrapper) RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error {
	return w.Client.RemoveFromQueue(id, removeFromClient, blocklist)
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
	arrClients map[string]ArrClient        // Map of arr app ID to client
	arrConfigs map[string]config.ArrConfig // Map of arr app ID to config (for removal options)
	trackers   []config.TrackerConfig      // Tracker configurations for filtering
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
func NewCleaner(cfg config.QueueCleanerConfig, qbtClient *qbittorrent.Client, arrClients map[string]ArrClient, arrConfigs map[string]config.ArrConfig, trackers []config.TrackerConfig, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		config:     cfg,
		qbtClient:  qbtClient,
		arrClients: arrClients,
		arrConfigs: arrConfigs,
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

// isImportFailed checks if an *arr queue item indicates import failure
// This should NOT match stalled downloads - those are handled by the stalled check
func isImportFailed(item QueueItem) bool {
	// Check Status field - only "error" status indicates failure, not "warning"
	// "warning" can be used for stalled downloads, so we need to check the actual message
	if item.Status == "error" {
		return true
	}

	// Check StatusMessages for specific import failure indicators
	// Look for titles that specifically indicate import/download failure
	for _, statusMsg := range item.StatusMessages {
		title := strings.ToLower(statusMsg.Title)
		// Check for specific failure titles
		if title == "import failed" || title == "download failed" {
			return true
		}
		// Exclude "download warning" which is used for stalled downloads
		if title == "download warning" {
			continue
		}
		// Check messages for import-specific failures
		for _, msg := range statusMsg.Messages {
			// Look for import/parsing specific errors, not connection/stall issues
			if contains(msg, "import", "parsing", "unable to parse", "cannot import", "failed to import") {
				// But exclude stalled/connection messages
				if !contains(msg, "stalled", "no connections", "connection", "timeout") {
					return true
				}
			}
		}
	}

	// Check ErrorMessage field - but only if it's about import/parsing, not connection issues
	if item.ErrorMessage != "" {
		// Only treat as import failure if it's about import/parsing, not connection/stall
		if contains(item.ErrorMessage, "import", "parsing", "unable to parse", "cannot import", "failed to import") {
			if !contains(item.ErrorMessage, "stalled", "no connections", "connection", "timeout") {
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
		itemsWithoutHash := 0
		for i := range queue.Records {
			item := queue.Records[i]
			if item.DownloadID != "" {
				// DownloadID in *arr apps is typically the torrent hash
				hash := item.DownloadID
				arrQueueMap[hash] = append(arrQueueMap[hash], arrQueueItem{
					arrID: arrID,
					item:  item,
				})
				totalQueueItems++
			} else {
				itemsWithoutHash++
			}
		}
		if itemsWithoutHash > 0 {
			c.logger.Debug("Queue items without DownloadID (hash)", "arr_id", arrID, "count", itemsWithoutHash)
		}
	}

	if len(arrQueueMap) == 0 {
		c.logger.Info("No queue items found in arr apps", "cleaner", c.config.Name, "checked_arrs", len(c.arrClients))
		return result, nil
	}

	// Get torrents from qBittorrent to check their status
	torrents, err := c.qbtClient.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("getting torrents: %w", err)
	}

	// Build map of hash to torrent for quick lookup
	// Use lowercase for case-insensitive matching (qBittorrent uses lowercase, Sonarr might use uppercase)
	torrentMap := make(map[string]*qbittorrent.Torrent)
	torrentMapLower := make(map[string]*qbittorrent.Torrent) // Lowercase hash -> torrent for case-insensitive lookup
	for i := range torrents {
		hash := torrents[i].Hash
		torrentMap[hash] = &torrents[i]
		torrentMapLower[strings.ToLower(hash)] = &torrents[i]
	}

	trackerIDs := make([]string, len(c.trackers))
	for i, t := range c.trackers {
		trackerIDs[i] = t.ID
	}
	c.logger.Info("Starting queue cleaner run", "cleaner", c.config.Name, "total_queue_items", totalQueueItems, "unique_hashes_from_arr", len(arrQueueMap), "configured_trackers", trackerIDs, "filter_mode", c.config.Trackers.FilterMode, "tracker_ids_from_config", c.config.Trackers.TrackerIDs, "qbittorrent_torrents", len(torrentMap))

	// Log sample hashes for debugging
	sampleHashes := make([]string, 0, 5)
	for hash := range arrQueueMap {
		if len(sampleHashes) < 5 {
			sampleHashes = append(sampleHashes, hash)
		}
	}
	if len(sampleHashes) > 0 {
		c.logger.Debug("Sample queue item hashes from arr", "hashes", sampleHashes)
	}

	// Track which torrents should be removed from *arr apps
	hashesToRemoveFromArr := make(map[string]bool)
	// Track which torrents should be deleted from bittorrent client
	// Map of hash -> reason for deletion
	hashesToDeleteFromClient := make(map[string]string)

	queueItemsProcessed := 0
	queueItemsFilteredOut := 0
	stalledStatesSeen := make(map[string]int) // Track what states we see for debugging

	// Iterate over queue items from *arr apps (source of truth)
	for hash, queueItems := range arrQueueMap {
		// Find the corresponding torrent in qBittorrent
		// Try exact match first, then case-insensitive match
		torrent, exists := torrentMap[hash]
		if !exists {
			// Try case-insensitive match (Sonarr might return uppercase, qBittorrent uses lowercase)
			torrent, exists = torrentMapLower[strings.ToLower(hash)]
			if !exists {
				c.logger.Info("Queue item has no corresponding torrent in bittorrent client", "hash", hash, "hash_lower", strings.ToLower(hash), "title", queueItems[0].item.Title, "total_torrents_in_client", len(torrentMap))
				queueItemsFilteredOut++
				continue
			}
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
			continue
		}

		// Log when torrent passes tracker filter (especially for slow downloads)
		if torrent.Dlspeed > 0 && torrent.Dlspeed < 102400 { // Less than 100KiB
			c.logger.Debug("Torrent passed tracker filter (slow download candidate)", "torrent", torrent.Name, "state", torrent.State, "tracker_url", torrent.Tracker, "speed", torrent.Dlspeed, "progress", torrent.Progress)
		}

		queueItemsProcessed++

		// Get or create strike tracking for this torrent
		// Use normalized hash (lowercase) for consistent key lookup
		// The hash from arrQueueMap (Sonarr) might be uppercase, while torrent.Hash (qBittorrent) is lowercase
		normalizedHash := strings.ToLower(hash)
		strikes := c.getOrCreateStrikes(normalizedHash)

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

				// Check if should delete from bittorrent client
				shouldDelete := c.config.Stalled.DeleteTorrent
				if !shouldDelete {
					// Check if tracker allows safe deletion below progress threshold
					trackerConfig := c.findTrackerConfig(torrent.Tracker)
					if trackerConfig != nil && trackerConfig.SafeDeleteProgress > 0 {
						// Progress is stored as 0.0-1.0, convert to percentage for comparison
						progressPercent := torrent.Progress * 100
						if progressPercent < trackerConfig.SafeDeleteProgress {
							shouldDelete = true
							c.logger.Info("Torrent marked for deletion due to safe_delete_progress threshold",
								"cleaner", c.config.Name,
								"torrent", torrent.Name,
								"progress", progressPercent,
								"safe_delete_progress", trackerConfig.SafeDeleteProgress,
								"tracker", trackerConfig.Name)
						}
					}
				}
				if shouldDelete {
					hashesToDeleteFromClient[hash] = fmt.Sprintf("stalled (%d strikes)", strikes.stalledStrikes)
				}
			}
		}

		// Check for slow download
		// Check any torrent that is downloading (not completed) and has download speed > 0
		// This includes "downloading" state and "stalledDL" state (stalled but still downloading)
		isDownloading := torrent.Progress < 1.0 && torrent.Dlspeed > 0
		if c.config.Slow != nil && isDownloading && c.config.Slow.MinSpeed.Bytes() > 0 {
			isSlow := torrent.Dlspeed < c.config.Slow.MinSpeed.Bytes()

			// Always log speed info for stalledDL/downloading torrents to debug
			if torrent.State == "stalledDL" || torrent.State == "downloading" {
				c.logger.Info("Checking slow download", "torrent", torrent.Name, "state", torrent.State, "speed", torrent.Dlspeed, "min_speed", c.config.Slow.MinSpeed.Bytes(), "progress", torrent.Progress, "is_slow", isSlow)
			} else if isSlow {
				c.logger.Info("Checking slow download", "torrent", torrent.Name, "state", torrent.State, "speed", torrent.Dlspeed, "min_speed", c.config.Slow.MinSpeed.Bytes(), "progress", torrent.Progress)
			}

			if isSlow {
				// For slow downloads, reset_on_progress means: reset strikes if speed improves above threshold
				// (i.e., torrent is no longer slow), not just if any progress is made
				// If speed is still below threshold, accumulate strikes even if progress is made
				c.strikesMu.Lock()
				strikes.slowStrikes++
				currentStrikes := strikes.slowStrikes
				c.strikesMu.Unlock()
				c.logger.Info("Incremented slow strikes", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", currentStrikes, "threshold", c.config.Slow.Strikes, "speed", torrent.Dlspeed, "min_speed", c.config.Slow.MinSpeed.Bytes(), "progress", torrent.Progress)
			} else {
				// Not slow (speed is above threshold), reset strikes
				// If reset_on_progress is enabled, also update last progress to track recovery
				if c.config.Slow.ResetOnProgress {
					c.strikesMu.Lock()
					oldStrikes := strikes.slowStrikes
					strikes.slowStrikes = 0
					strikes.lastProgress = torrent.Progress
					c.strikesMu.Unlock()
					if oldStrikes > 0 {
						c.logger.Info("Reset slow strikes - speed improved above threshold", "cleaner", c.config.Name, "torrent", torrent.Name, "old_strikes", oldStrikes, "speed", torrent.Dlspeed, "min_speed", c.config.Slow.MinSpeed.Bytes())
					}
				} else {
					c.strikesMu.Lock()
					strikes.slowStrikes = 0
					c.strikesMu.Unlock()
				}
			}

			// Update last progress for tracking (used by stalled check, not slow check)
			c.strikesMu.Lock()
			strikes.lastProgress = torrent.Progress
			c.strikesMu.Unlock()

			if strikes.slowStrikes >= c.config.Slow.Strikes {
				hashesToRemoveFromArr[hash] = true
				c.logger.Info("Torrent marked for removal from arr (slow)", "cleaner", c.config.Name, "torrent", torrent.Name, "strikes", strikes.slowStrikes, "speed", torrent.Dlspeed)
				result.RemovedItems = append(result.RemovedItems, fmt.Sprintf("%s (slow, %d strikes, %d bytes/s)", torrent.Name, strikes.slowStrikes, torrent.Dlspeed))

				// Check if should delete from bittorrent client
				shouldDelete := c.config.Slow.DeleteTorrent
				if !shouldDelete {
					// Check if tracker allows safe deletion below progress threshold
					trackerConfig := c.findTrackerConfig(torrent.Tracker)
					if trackerConfig != nil && trackerConfig.SafeDeleteProgress > 0 {
						// Progress is stored as 0.0-1.0, convert to percentage for comparison
						progressPercent := torrent.Progress * 100
						if progressPercent < trackerConfig.SafeDeleteProgress {
							shouldDelete = true
							c.logger.Info("Torrent marked for deletion due to safe_delete_progress threshold",
								"cleaner", c.config.Name,
								"torrent", torrent.Name,
								"progress", progressPercent,
								"safe_delete_progress", trackerConfig.SafeDeleteProgress,
								"tracker", trackerConfig.Name)
						}
					}
				}
				if shouldDelete {
					hashesToDeleteFromClient[hash] = fmt.Sprintf("slow (%d strikes)", strikes.slowStrikes)
				}
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

				// Check if should delete from bittorrent client
				shouldDelete := c.config.FailedImport.DeleteTorrent
				if !shouldDelete {
					// Check if tracker allows safe deletion below progress threshold
					trackerConfig := c.findTrackerConfig(torrent.Tracker)
					if trackerConfig != nil && trackerConfig.SafeDeleteProgress > 0 {
						// Progress is stored as 0.0-1.0, convert to percentage for comparison
						progressPercent := torrent.Progress * 100
						if progressPercent < trackerConfig.SafeDeleteProgress {
							shouldDelete = true
							c.logger.Info("Torrent marked for deletion due to safe_delete_progress threshold",
								"cleaner", c.config.Name,
								"torrent", torrent.Name,
								"progress", progressPercent,
								"safe_delete_progress", trackerConfig.SafeDeleteProgress,
								"tracker", trackerConfig.Name)
						}
					}
				}
				if shouldDelete {
					hashesToDeleteFromClient[hash] = fmt.Sprintf("failed import (%d strikes)", strikes.failedImportStrikes)
				}
			}
		}
	}

	c.logger.Info("Finished processing queue items", "cleaner", c.config.Name, "total_queue_items", totalQueueItems, "queue_items_filtered_out", queueItemsFilteredOut, "queue_items_processed", queueItemsProcessed, "queue_items_to_remove_from_arr", len(hashesToRemoveFromArr))

	// Clean up strikes for torrents that no longer exist in both the queue AND qBittorrent
	// Don't clean up strikes for torrents that are still in qBittorrent but temporarily removed from queue
	// This prevents strikes from being reset when Sonarr removes/re-adds stalled torrents
	c.strikesMu.Lock()
	normalizedArrHashes := make(map[string]bool)
	for hash := range arrQueueMap {
		normalizedArrHashes[strings.ToLower(hash)] = true
	}
	// Build normalized qBittorrent hashes map for checking if torrent still exists
	normalizedQbtHashes := make(map[string]bool)
	for hash := range torrentMap {
		normalizedQbtHashes[strings.ToLower(hash)] = true
	}
	for hash := range torrentMapLower {
		normalizedQbtHashes[hash] = true // Already lowercase
	}
	for hash := range c.strikes {
		// Only delete strikes if torrent is not in queue AND not in qBittorrent
		if !normalizedArrHashes[hash] && !normalizedQbtHashes[hash] {
			delete(c.strikes, hash)
		}
	}
	c.strikesMu.Unlock()

	// Remove from *arr apps
	arrRemovedCount := 0
	for hash := range hashesToRemoveFromArr {
		if queueItems, ok := arrQueueMap[hash]; ok {
			for _, qItem := range queueItems {
				// Only remove from arr apps that are configured for this cleaner
				if !c.isArrConfigured(qItem.arrID) {
					continue
				}

				// Get removal options from arr config
				arrConfig, hasConfig := c.arrConfigs[qItem.arrID]
				removeFromClient := false
				blocklist := false
				if hasConfig {
					removeFromClient = arrConfig.RemoveFromClient
					blocklist = arrConfig.Blocklist
				}

				if c.config.DryRun {
					c.logger.Info("DRY RUN - Would remove item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID, "remove_from_client", removeFromClient, "blocklist", blocklist)
				} else {
					arrClient := c.arrClients[qItem.arrID]
					if arrClient != nil {
						// Use removal options from arr config
						if err := arrClient.RemoveFromQueue(qItem.item.ID, removeFromClient, blocklist); err != nil {
							c.logger.Warn("Failed to remove item from arr queue", "cleaner", c.config.Name, "item_id", qItem.item.ID, "arr_id", qItem.arrID, "error", err)
							result.Errors = append(result.Errors, fmt.Sprintf("failed to remove from %s queue: %v", qItem.arrID, err))
						} else {
							arrRemovedCount++
							c.logger.Info("Removed item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID, "remove_from_client", removeFromClient, "blocklist", blocklist)
						}
					}
				}
			}
		}
	}
	result.RemovedCount = arrRemovedCount

	// Delete from bittorrent client if configured
	if len(hashesToDeleteFromClient) > 0 {
		hashesToDelete := make([]string, 0, len(hashesToDeleteFromClient))
		for hash := range hashesToDeleteFromClient {
			hashesToDelete = append(hashesToDelete, hash)
		}

		if c.config.DryRun {
			for hash, reason := range hashesToDeleteFromClient {
				torrentName := hash
				if torrent, exists := torrentMap[hash]; exists {
					torrentName = torrent.Name
				} else if torrent, exists := torrentMapLower[strings.ToLower(hash)]; exists {
					torrentName = torrent.Name
				}
				c.logger.Info("DRY RUN - Would delete torrent from bittorrent client", "cleaner", c.config.Name, "torrent", torrentName, "hash", hash, "reason", reason)
			}
		} else {
			// Delete torrents from bittorrent client (without deleting files)
			if err := c.qbtClient.DeleteTorrent(hashesToDelete, false); err != nil {
				c.logger.Warn("Failed to delete torrents from bittorrent client", "cleaner", c.config.Name, "error", err)
				result.Errors = append(result.Errors, fmt.Sprintf("failed to delete from bittorrent client: %v", err))
			} else {
				for hash, reason := range hashesToDeleteFromClient {
					torrentName := hash
					if torrent, exists := torrentMap[hash]; exists {
						torrentName = torrent.Name
					} else if torrent, exists := torrentMapLower[strings.ToLower(hash)]; exists {
						torrentName = torrent.Name
					}
					c.logger.Info("Deleted torrent from bittorrent client", "cleaner", c.config.Name, "torrent", torrentName, "hash", hash, "reason", reason)
				}
			}
		}
	}

	return result, nil
}

// matchesTrackerFilter checks if a tracker URL matches the configured tracker filters
func (c *Cleaner) matchesTrackerFilter(trackerURL string) bool {
	// If no trackers configured or no tracker IDs specified, match all
	if len(c.trackers) == 0 || len(c.config.Trackers.TrackerIDs) == 0 {
		return true
	}

	// If filter_mode is empty, no filtering
	if c.config.Trackers.FilterMode == "" {
		return true
	}

	// Build a map of configured tracker IDs for quick lookup
	configuredTrackerIDs := make(map[string]bool)
	for _, id := range c.config.Trackers.TrackerIDs {
		configuredTrackerIDs[id] = true
	}

	// Check if tracker URL matches any of the configured tracker regex patterns
	// Only check trackers that are in the configured tracker IDs list
	matches := false
	for _, tracker := range c.trackers {
		// Only check trackers that are in the configured list
		if !configuredTrackerIDs[tracker.ID] {
			continue
		}

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
			c.logger.Debug("Tracker URL matched", "tracker_url", trackerURL, "tracker_id", tracker.ID, "tracker_name", tracker.Name, "pattern", tracker.URLRegex)
			break
		} else {
			c.logger.Debug("Tracker URL did not match", "tracker_url", trackerURL, "tracker_id", tracker.ID, "pattern", tracker.URLRegex)
		}
	}

	if c.config.Trackers.FilterMode == "include" {
		return matches
	}
	// exclude mode
	return !matches
}
