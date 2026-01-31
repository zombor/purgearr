package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/zombor/purgearr/internal/clients/lidarr"
	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/radarr"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
	queuecleaner "github.com/zombor/purgearr/internal/cleaners/queue"
	downloadcleaner "github.com/zombor/purgearr/internal/cleaners/download"
	"github.com/zombor/purgearr/internal/scheduler"
)

// Handler holds dependencies for API handlers
type Handler struct {
	config         *config.Config
	scheduler      *scheduler.Scheduler
	qbtClient      *qbittorrent.Client
	sonarrClient   *sonarr.Client
	radarrClient   *radarr.Client
	lidarrClient   *lidarr.Client
	logger         *slog.Logger
	configPath     string
}

// NewHandler creates a new API handler
func NewHandler(cfg *config.Config, sched *scheduler.Scheduler, qbtClient *qbittorrent.Client, sonarrClient *sonarr.Client, radarrClient *radarr.Client, lidarrClient *lidarr.Client, logger *slog.Logger, configPath string) *Handler {
	return &Handler{
		config:       cfg,
		scheduler:    sched,
		qbtClient:    qbtClient,
		sonarrClient: sonarrClient,
		radarrClient: radarrClient,
		lidarrClient: lidarrClient,
		logger:       logger,
		configPath:   configPath,
	}
}

// GetConfig returns the current configuration
func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(h.config); err != nil {
		h.logger.Error("Error encoding config", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// UpdateConfig updates the configuration
func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	var newConfig config.Config
	if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON: %v", err), http.StatusBadRequest)
		return
	}

	if err := newConfig.Validate(); err != nil {
		http.Error(w, fmt.Sprintf("Invalid configuration: %v", err), http.StatusBadRequest)
		return
	}

	// Save to file
	if err := newConfig.Save(h.configPath); err != nil {
		h.logger.Error("Error saving config", "error", err)
		http.Error(w, "Failed to save configuration", http.StatusInternalServerError)
		return
	}

	h.config = &newConfig

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// GetStatus returns the status of all jobs
func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	jobs := h.scheduler.GetAllJobs()
	
	// Count enabled clients
	qbtEnabled := 0
	sonarrEnabled := 0
	radarrEnabled := 0
	lidarrEnabled := 0
	for _, client := range h.config.BittorrentClients {
		if client.Enabled && client.Kind == "qbittorrent" {
			qbtEnabled++
		}
	}
	for _, arr := range h.config.Arrs {
		if arr.Enabled {
			switch arr.Kind {
			case "sonarr":
				sonarrEnabled++
			case "radarr":
				radarrEnabled++
			case "lidarr":
				lidarrEnabled++
			}
		}
	}

	status := map[string]interface{}{
		"jobs": jobs,
		"clients": map[string]interface{}{
			"qbittorrent": map[string]interface{}{
				"count":   qbtEnabled,
				"enabled": qbtEnabled,
			},
			"sonarr": map[string]interface{}{
				"count":   sonarrEnabled,
				"enabled": sonarrEnabled,
			},
			"radarr": map[string]interface{}{
				"count":   radarrEnabled,
				"enabled": radarrEnabled,
			},
			"lidarr": map[string]interface{}{
				"count":   lidarrEnabled,
				"enabled": lidarrEnabled,
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		h.logger.Error("Error encoding status", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// RunQueueCleaner runs a queue cleaner manually
func (h *Handler) RunQueueCleaner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing cleaner ID", http.StatusBadRequest)
		return
	}

	// Prefix the ID with "queue-" to match the job ID format
	jobID := "queue-" + id
	if err := h.scheduler.RunJobNow(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// RunDownloadCleaner runs a download cleaner manually
func (h *Handler) RunDownloadCleaner(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing cleaner ID", http.StatusBadRequest)
		return
	}

	// Prefix the ID with "download-" to match the job ID format
	jobID := "download-" + id
	if err := h.scheduler.RunJobNow(jobID); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// RunMalwareBlocker runs the malware blocker manually
func (h *Handler) RunMalwareBlocker(w http.ResponseWriter, r *http.Request) {
	if err := h.scheduler.RunJobNow("malware-blocker"); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// TestSonarrConnection tests the Sonarr connection
func (h *Handler) TestSonarrConnection(w http.ResponseWriter, r *http.Request) {
	if h.sonarrClient == nil {
		http.Error(w, "Sonarr client not configured", http.StatusBadRequest)
		return
	}

	status, err := h.sonarrClient.GetSystemStatus()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"version": status.Version,
	})
}

// TestRadarrConnection tests the Radarr connection
func (h *Handler) TestRadarrConnection(w http.ResponseWriter, r *http.Request) {
	if h.radarrClient == nil {
		http.Error(w, "Radarr client not configured", http.StatusBadRequest)
		return
	}

	status, err := h.radarrClient.GetSystemStatus()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"version": status.Version,
	})
}

// TestLidarrConnection tests the Lidarr connection
func (h *Handler) TestLidarrConnection(w http.ResponseWriter, r *http.Request) {
	if h.lidarrClient == nil {
		http.Error(w, "Lidarr client not configured", http.StatusBadRequest)
		return
	}

	status, err := h.lidarrClient.GetSystemStatus()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"version": status.Version,
	})
}

// TestQBittorrentConnection tests the qBittorrent connection
func (h *Handler) TestQBittorrentConnection(w http.ResponseWriter, r *http.Request) {
	if h.qbtClient == nil {
		http.Error(w, "qBittorrent client not configured", http.StatusBadRequest)
		return
	}

	version, err := h.qbtClient.GetAppVersion()
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"version": version,
	})
}

// HealthCheck returns a simple health check response
func (h *Handler) HealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "ok",
		"time":   time.Now().Format(time.RFC3339),
	})
}

// GetTorrentStatus returns the current status of torrents being processed by queue cleaners
func (h *Handler) GetTorrentStatus(w http.ResponseWriter, r *http.Request) {
	allStatuses := make(map[string][]queuecleaner.TorrentStatus)

	// Get all jobs from scheduler
	jobs := h.scheduler.GetAllJobs()
	for jobID := range jobs {
		// Only process queue cleaner jobs
		if !strings.HasPrefix(jobID, "queue-") {
			continue
		}

		// Get the job and try to cast it to queue.Job
		job, ok := h.scheduler.GetJob(jobID)
		if !ok {
			continue
		}

		// Type assert to queue.Job
		queueJob, ok := job.(*queuecleaner.Job)
		if !ok {
			continue
		}

		// Get the cleaner and its status
		cleaner := queueJob.GetCleaner()
		statuses, err := cleaner.GetStrikesStatus()
		if err != nil {
			h.logger.Warn("Failed to get strikes status", "cleaner", jobID, "error", err)
			continue
		}

		// Extract cleaner ID from job ID (remove "queue-" prefix)
		cleanerID := strings.TrimPrefix(jobID, "queue-")
		allStatuses[cleanerID] = statuses
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(allStatuses); err != nil {
		h.logger.Error("Error encoding torrent status", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// GetDownloadCleanerStatus returns candidate torrents for a download cleaner
func (h *Handler) GetDownloadCleanerStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		http.Error(w, "Missing cleaner ID", http.StatusBadRequest)
		return
	}

	// Prefix the ID with "download-" to match the job ID format
	jobID := "download-" + id
	
	// Get the job from scheduler
	job, ok := h.scheduler.GetJob(jobID)
	if !ok {
		http.Error(w, "Download cleaner not found", http.StatusNotFound)
		return
	}

	// Type assert to download.Job
	downloadJob, ok := job.(*downloadcleaner.Job)
	if !ok {
		http.Error(w, "Invalid job type", http.StatusInternalServerError)
		return
	}

	// Get the cleaner and its candidate torrents
	cleaner := downloadJob.GetCleaner()
	candidates, err := cleaner.GetCandidateTorrents()
	if err != nil {
		h.logger.Error("Failed to get candidate torrents", "cleaner", id, "error", err)
		http.Error(w, fmt.Sprintf("Failed to get candidate torrents: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(candidates); err != nil {
		h.logger.Error("Error encoding download cleaner status", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

