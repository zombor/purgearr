package qbittorrent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client represents a qBittorrent API client
type Client struct {
	url      string
	username string
	password string
	client   *http.Client
	loggedIn bool
}

// NewClient creates a new qBittorrent client
func NewClient(url, username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		url:      url,
		username: username,
		password: password,
		client: &http.Client{
			Timeout: 30 * time.Second,
			Jar:     jar,
		},
	}
}

// Login authenticates with qBittorrent
func (c *Client) Login() error {
	loginURL := fmt.Sprintf("%s/api/v2/auth/login", c.url)

	data := url.Values{}
	data.Set("username", c.username)
	data.Set("password", c.password)

	req, err := http.NewRequest("POST", loginURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	responseText := strings.TrimSpace(string(body))
	if responseText != "Ok." {
		return fmt.Errorf("login failed: %s", responseText)
	}

	c.loggedIn = true
	return nil
}

// ensureLoggedIn ensures the client is logged in
func (c *Client) ensureLoggedIn() error {
	if !c.loggedIn {
		return c.Login()
	}
	return nil
}

// Torrent represents a torrent in qBittorrent
type Torrent struct {
	Hash              string  `json:"hash"`
	Name              string  `json:"name"`
	Size              int64   `json:"size"`
	Progress          float64 `json:"progress"`
	Dlspeed           int64   `json:"dlspeed"`
	Upspeed           int64   `json:"upspeed"`
	Priority          int     `json:"priority"`
	NumSeeds          int     `json:"num_seeds"`
	NumLeechers       int     `json:"num_leechers"`
	Ratio             float64 `json:"ratio"`
	Eta               int64   `json:"eta"`
	State             string  `json:"state"`
	Category          string  `json:"category"`
	Tags              string  `json:"tags"`
	AddedOn           int64   `json:"added_on"`
	CompletionOn      int64   `json:"completion_on"`
	Tracker           string  `json:"tracker"`
	Downloaded        int64   `json:"downloaded"`
	Uploaded          int64   `json:"uploaded"`
	DownloadedSession int64   `json:"downloaded_session"`
	UploadedSession   int64   `json:"uploaded_session"`
	SeedingTime       int64   `json:"seeding_time"`
	TimeActive        int64   `json:"time_active"`
}

// GetTorrents retrieves all torrents from qBittorrent
func (c *Client) GetTorrents() ([]Torrent, error) {
	if err := c.ensureLoggedIn(); err != nil {
		return nil, err
	}

	torrentsURL := fmt.Sprintf("%s/api/v2/torrents/info", c.url)

	req, err := http.NewRequest("GET", torrentsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		c.loggedIn = false
		if err := c.Login(); err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		return c.GetTorrents()
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var torrents []Torrent
	if err := json.NewDecoder(resp.Body).Decode(&torrents); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return torrents, nil
}

// DeleteTorrent deletes a torrent from qBittorrent
func (c *Client) DeleteTorrent(hashes []string, deleteFiles bool) error {
	if err := c.ensureLoggedIn(); err != nil {
		return err
	}

	deleteURL := fmt.Sprintf("%s/api/v2/torrents/delete", c.url)

	data := url.Values{}
	data.Set("hashes", strings.Join(hashes, "|"))
	data.Set("deleteFiles", strconv.FormatBool(deleteFiles))

	req, err := http.NewRequest("POST", deleteURL, strings.NewReader(data.Encode()))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		c.loggedIn = false
		if err := c.Login(); err != nil {
			return fmt.Errorf("re-authenticating: %w", err)
		}
		return c.DeleteTorrent(hashes, deleteFiles)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetTorrentProperties retrieves detailed properties for a torrent
func (c *Client) GetTorrentProperties(hash string) (map[string]interface{}, error) {
	if err := c.ensureLoggedIn(); err != nil {
		return nil, err
	}

	propsURL := fmt.Sprintf("%s/api/v2/torrents/properties?hash=%s", c.url, hash)

	req, err := http.NewRequest("GET", propsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		c.loggedIn = false
		if err := c.Login(); err != nil {
			return nil, fmt.Errorf("re-authenticating: %w", err)
		}
		return c.GetTorrentProperties(hash)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	var props map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&props); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return props, nil
}

// AppVersion represents qBittorrent version info
type AppVersion struct {
	Version string `json:"version"`
}

// GetAppVersion checks if qBittorrent is accessible
func (c *Client) GetAppVersion() (string, error) {
	versionURL := fmt.Sprintf("%s/api/v2/app/version", c.url)

	req, err := http.NewRequest("GET", versionURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("making request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected status code %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("reading response: %w", err)
	}

	return strings.TrimSpace(string(body)), nil
}
