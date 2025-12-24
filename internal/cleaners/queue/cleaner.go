package queue

import (
	"fmt"
	"log/slog"
	"regexp"
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

// Cleaner handles queue cleaning operations
type Cleaner struct {
	config     config.QueueCleanerConfig
	qbtClient  *qbittorrent.Client
	arrClients map[string]ArrClient   // Map of arr app ID to client
	trackers   []config.TrackerConfig // Tracker configurations for filtering
	logger     *slog.Logger
}

// NewCleaner creates a new queue cleaner
func NewCleaner(cfg config.QueueCleanerConfig, qbtClient *qbittorrent.Client, arrClients map[string]ArrClient, trackers []config.TrackerConfig, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		config:     cfg,
		qbtClient:  qbtClient,
		arrClients: arrClients,
		trackers:   trackers,
		logger:     logger,
	}
}

// CleanResult represents the result of a cleaning operation
type CleanResult struct {
	DryRun       bool     `json:"dry_run"`
	RemovedCount int      `json:"removed_count"`
	RemovedItems []string `json:"removed_items"`
	Errors       []string `json:"errors"`
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

	// Get torrents from qBittorrent
	torrents, err := c.qbtClient.GetTorrents()
	if err != nil {
		return nil, fmt.Errorf("getting torrents: %w", err)
	}

	// Get queues from all configured *arr apps
	// Map of torrent hash to queue items (hash -> []*arrQueueItem)
	arrQueueMap := make(map[string][]arrQueueItem)

	for arrID, arrClient := range c.arrClients {
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
			}
		}
	}

	now := time.Now()
	hashesToDelete := []string{}

	for _, torrent := range torrents {
		// Check tracker filter
		if !c.matchesTrackerFilter(torrent.Tracker) {
			continue
		}

		shouldRemove := false
		reason := ""

		// Check if torrent should be removed based on configuration
		if c.config.RemoveFailed {
			if torrent.State == "error" {
				shouldRemove = true
				reason = "failed"
			}
		}

		if c.config.RemoveStalled && !shouldRemove {
			// Check if torrent is stalled (no progress for max age)
			if torrent.State == "downloading" || torrent.State == "stalledDL" {
				// We need to check if it's been stalled for too long
				// Since we don't have last activity time directly, we'll use ETA
				// If ETA is very high or -1, and speed is 0, it's likely stalled
				if torrent.Dlspeed == 0 && torrent.Eta > 86400 { // ETA > 1 day with 0 speed
					shouldRemove = true
					reason = "stalled"
				}
			}
		}

		if c.config.RemoveSlow && !shouldRemove {
			// Check if torrent is slow (below min speed)
			if torrent.State == "downloading" && c.config.MinSpeed > 0 {
				if torrent.Dlspeed < c.config.MinSpeed {
					shouldRemove = true
					reason = "slow"
				}
			}
		}

		// Check max age for stalled downloads
		if c.config.MaxAge.Duration() > 0 && !shouldRemove {
			if torrent.State == "downloading" || torrent.State == "stalledDL" {
				addedTime := time.Unix(torrent.AddedOn, 0)
				if now.Sub(addedTime) > c.config.MaxAge.Duration() {
					shouldRemove = true
					reason = "max_age"
				}
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

			// Log what would be removed from *arr apps in dry run mode
			for _, hash := range hashesToDelete {
				if queueItems, ok := arrQueueMap[hash]; ok {
					for _, qItem := range queueItems {
						c.logger.Info("DRY RUN - Would remove item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID)
					}
				}
			}
		} else {
			if err := c.qbtClient.DeleteTorrent(hashesToDelete, false); err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("failed to delete torrents: %v", err))
				c.logger.Error("Error deleting torrents", "cleaner", c.config.Name, "error", err)
			} else {
				result.RemovedCount = len(hashesToDelete)
			}

			// Remove from *arr app queues if applicable
			for _, hash := range hashesToDelete {
				if queueItems, ok := arrQueueMap[hash]; ok {
					for _, qItem := range queueItems {
						arrClient := c.arrClients[qItem.arrID]
						if arrClient != nil {
							if err := arrClient.RemoveFromQueue(qItem.item.ID, true, false); err != nil {
								c.logger.Warn("Failed to remove item from arr queue", "cleaner", c.config.Name, "item_id", qItem.item.ID, "arr_id", qItem.arrID, "error", err)
								result.Errors = append(result.Errors, fmt.Sprintf("failed to remove from %s queue: %v", qItem.arrID, err))
							} else {
								c.logger.Info("Removed item from arr queue", "cleaner", c.config.Name, "item", qItem.item.Title, "item_id", qItem.item.ID, "arr_id", qItem.arrID)
							}
						}
					}
				}
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
