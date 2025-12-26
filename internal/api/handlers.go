package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
	"github.com/zombor/purgearr/internal/scheduler"
)

// Handler holds dependencies for API handlers
type Handler struct {
	config         *config.Config
	scheduler      *scheduler.Scheduler
	qbtClient      *qbittorrent.Client
	sonarrClient   *sonarr.Client
	logger         *slog.Logger
	configPath     string
}

// NewHandler creates a new API handler
func NewHandler(cfg *config.Config, sched *scheduler.Scheduler, qbtClient *qbittorrent.Client, sonarrClient *sonarr.Client, logger *slog.Logger, configPath string) *Handler {
	return &Handler{
		config:      cfg,
		scheduler:   sched,
		qbtClient:   qbtClient,
		sonarrClient: sonarrClient,
		logger:      logger,
		configPath:  configPath,
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
	for _, client := range h.config.BittorrentClients {
		if client.Enabled && client.Kind == "qbittorrent" {
			qbtEnabled++
		}
	}
	for _, arr := range h.config.Arrs {
		if arr.Enabled && arr.Kind == "sonarr" {
			sonarrEnabled++
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

	if err := h.scheduler.RunJobNow(id); err != nil {
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

	if err := h.scheduler.RunJobNow(id); err != nil {
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

