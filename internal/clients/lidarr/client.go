package lidarr

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client represents a Lidarr API client
type Client struct {
	url    string
	apiKey string
	client *http.Client
}

// NewClient creates a new Lidarr client
func NewClient(url, apiKey string) *Client {
	return &Client{
		url:    url,
		apiKey: apiKey,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// QueueItem represents an item in Lidarr's queue
type QueueItem struct {
	ID                      int             `json:"id"`
	ArtistID                int             `json:"artistId"`
	AlbumID                 int             `json:"albumId"`
	Title                   string          `json:"title"`
	Status                  string          `json:"status"`
	StatusMessages          []StatusMessage `json:"statusMessages"`
	ErrorMessage            string          `json:"errorMessage"`
	Timeleft                string          `json:"timeleft"`
	EstimatedCompletionTime time.Time       `json:"estimatedCompletionTime"`
	Protocol                string          `json:"protocol"`
	DownloadClient          string          `json:"downloadClient"`
	DownloadID              string          `json:"downloadId"`
	Size                    int64           `json:"size"`
	Sizeleft                int64           `json:"sizeleft"`
	DownloadedBytes         int64           `json:"downloadedBytes"`
}

// StatusMessage represents a status message for a queue item
type StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// QueueResponse represents the response from Lidarr's queue endpoint
type QueueResponse struct {
	Page          int         `json:"page"`
	PageSize      int         `json:"pageSize"`
	SortKey       string      `json:"sortKey"`
	SortDirection string      `json:"sortDirection"`
	TotalRecords  int         `json:"totalRecords"`
	Records       []QueueItem `json:"records"`
}

// GetQueue retrieves the current queue from Lidarr
// Handles pagination to fetch all queue items
func (c *Client) GetQueue() (*QueueResponse, error) {
	var allRecords []QueueItem
	page := 1
	pageSize := 100 // Fetch up to 100 items per page

	for {
		// Include unknown status items to ensure failed imports and other status items are included
		url := fmt.Sprintf("%s/api/v1/queue?page=%d&pageSize=%d&includeUnknown=true", c.url, page, pageSize)

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("creating request: %w", err)
		}

		req.Header.Set("X-Api-Key", c.apiKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("making request: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
		}

		var queue QueueResponse
		if err := json.NewDecoder(resp.Body).Decode(&queue); err != nil {
			return nil, fmt.Errorf("decoding response: %w", err)
		}

		// Append records from this page
		allRecords = append(allRecords, queue.Records...)

		// Check if we've fetched all records
		if len(allRecords) >= queue.TotalRecords || len(queue.Records) == 0 {
			break
		}

		page++
	}

	// Return a response with all records combined
	return &QueueResponse{
		Page:          1,
		PageSize:      len(allRecords),
		SortKey:       "",
		SortDirection: "",
		TotalRecords:  len(allRecords),
		Records:       allRecords,
	}, nil
}

// RemoveFromQueue removes an item from Lidarr's queue
// removeFromClient: if true, removes from download client; if false, ignores the download (doesn't remove from client)
// blocklist: if true, blocks the release from being redownloaded
func (c *Client) RemoveFromQueue(id int, removeFromClient bool, blocklist bool) error {
	// Build URL with query parameters
	url := fmt.Sprintf("%s/api/v1/queue/%d?removeFromClient=%t&blocklist=%t", c.url, id, removeFromClient, blocklist)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("X-Api-Key", c.apiKey)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SystemStatus represents Lidarr's system status
type SystemStatus struct {
	Version string `json:"version"`
}

// GetSystemStatus checks if Lidarr is accessible
func (c *Client) GetSystemStatus() (*SystemStatus, error) {
	url := fmt.Sprintf("%s/api/v1/system/status", c.url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var status SystemStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return &status, nil
}
