package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/zombor/purgearr/internal/api"
	downloadcleaner "github.com/zombor/purgearr/internal/cleaners/download"
	queuecleaner "github.com/zombor/purgearr/internal/cleaners/queue"
	"github.com/zombor/purgearr/internal/clients/lidarr"
	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/clients/radarr"
	"github.com/zombor/purgearr/internal/clients/sonarr"
	"github.com/zombor/purgearr/internal/config"
	"github.com/zombor/purgearr/internal/malwareblocker"
	"github.com/zombor/purgearr/internal/scheduler"
)

func main() {
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	logFormat := flag.String("log-format", "text", "Log format: 'text' or 'json'")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	// Setup logger
	logLevel := slog.LevelInfo
	if *debug {
		logLevel = slog.LevelDebug
	}

	var logHandler slog.Handler
	switch *logFormat {
	case "json":
		logHandler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: false,
			Level:     logLevel,
		})
	case "text":
		logHandler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			AddSource: false,
			Level:     logLevel,
		})
	default:
		fmt.Fprintf(os.Stderr, "Invalid log format: %s. Must be 'text' or 'json'\n", *logFormat)
		os.Exit(1)
	}
	logger := slog.New(logHandler)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		logger.Error("Failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Initialize clients - create maps of clients by ID
	qbtClients := make(map[string]*qbittorrent.Client)
	for _, clientCfg := range cfg.BittorrentClients {
		if !clientCfg.Enabled {
			continue
		}
		// Only process qBittorrent clients for now
		if clientCfg.Kind != "qbittorrent" {
			continue
		}
		client := qbittorrent.NewClient(
			clientCfg.URL,
			clientCfg.Username,
			clientCfg.Password,
		)
		// Test connection
		if err := client.Login(); err != nil {
			logger.Warn("Failed to connect to qBittorrent", "id", clientCfg.ID, "error", err)
		} else {
			logger.Info("Connected to qBittorrent", "id", clientCfg.ID)
		}
		qbtClients[clientCfg.ID] = client
	}

	sonarrClients := make(map[string]*sonarr.Client)
	radarrClients := make(map[string]*radarr.Client)
	lidarrClients := make(map[string]*lidarr.Client)
	for _, arrCfg := range cfg.Arrs {
		if !arrCfg.Enabled {
			continue
		}
		switch arrCfg.Kind {
		case "sonarr":
			client := sonarr.NewClient(
				arrCfg.URL,
				arrCfg.APIKey,
			)
			// Test connection
			if _, err := client.GetSystemStatus(); err != nil {
				logger.Warn("Failed to connect to Sonarr", "id", arrCfg.ID, "error", err)
			} else {
				logger.Info("Connected to Sonarr", "id", arrCfg.ID)
			}
			sonarrClients[arrCfg.ID] = client
		case "radarr":
			client := radarr.NewClient(
				arrCfg.URL,
				arrCfg.APIKey,
			)
			// Test connection
			if _, err := client.GetSystemStatus(); err != nil {
				logger.Warn("Failed to connect to Radarr", "id", arrCfg.ID, "error", err)
			} else {
				logger.Info("Connected to Radarr", "id", arrCfg.ID)
			}
			radarrClients[arrCfg.ID] = client
		case "lidarr":
			client := lidarr.NewClient(
				arrCfg.URL,
				arrCfg.APIKey,
			)
			// Test connection
			if _, err := client.GetSystemStatus(); err != nil {
				logger.Warn("Failed to connect to Lidarr", "id", arrCfg.ID, "error", err)
			} else {
				logger.Info("Connected to Lidarr", "id", arrCfg.ID)
			}
			lidarrClients[arrCfg.ID] = client
		}
	}

	// Initialize scheduler
	sched := scheduler.NewScheduler(logger)

	// Setup malware blocker if enabled
	if cfg.MalwareBlocker.Enabled {
		if cfg.MalwareBlocker.Schedule == "" {
			logger.Warn("Malware blocker enabled but no schedule specified")
		} else {
			// Use first enabled qBittorrent client for malware blocker
			var blockerQBTClient *qbittorrent.Client
			for _, clientCfg := range cfg.BittorrentClients {
				if clientCfg.Enabled && clientCfg.Kind == "qbittorrent" {
					blockerQBTClient = qbtClients[clientCfg.ID]
					break
				}
			}

			if blockerQBTClient == nil {
				logger.Warn("Malware blocker enabled but no qBittorrent client found")
			} else {
				// Get trackers for malware blocker
				trackers := cfg.GetTrackersByIDs(cfg.MalwareBlocker.Trackers.TrackerIDs)
				blocker := malwareblocker.NewBlocker(cfg.MalwareBlocker, blockerQBTClient, trackers, logger)
				job := malwareblocker.NewJob(blocker, cfg.MalwareBlocker)
				if err := sched.AddJob(job, cfg.MalwareBlocker.Schedule); err != nil {
					logger.Error("Failed to schedule malware blocker", "error", err)
				}
			}
		}
	}

	// Setup queue cleaners
	for _, qcConfig := range cfg.QueueCleaners {
		if !qcConfig.Enabled {
			continue
		}

		// Get bittorrent clients (use specified ones or all enabled if empty)
		bittorrentClientIDs := qcConfig.Bittorrent
		if len(bittorrentClientIDs) == 0 {
			// Use all enabled bittorrent clients
			for _, clientCfg := range cfg.BittorrentClients {
				if clientCfg.Enabled {
					bittorrentClientIDs = append(bittorrentClientIDs, clientCfg.ID)
				}
			}
		}
		if len(bittorrentClientIDs) == 0 {
			logger.Warn("Queue cleaner requires at least one bittorrent client but none found", "cleaner", qcConfig.Name)
			continue
		}

		// Get arr app clients (use specified ones or all enabled if empty)
		arrClientIDs := qcConfig.Arrs
		if len(arrClientIDs) == 0 {
			// Use all enabled arr apps
			for _, arrCfg := range cfg.Arrs {
				if arrCfg.Enabled {
					arrClientIDs = append(arrClientIDs, arrCfg.ID)
				}
			}
		}

		// Build map of arr clients for this cleaner
		arrClients := make(map[string]queuecleaner.ArrClient)
		arrConfigs := make(map[string]config.ArrConfig)
		for _, arrID := range arrClientIDs {
			// Find the arr config to determine the kind
			var arrCfg *config.ArrConfig
			for i := range cfg.Arrs {
				if cfg.Arrs[i].ID == arrID {
					arrCfg = &cfg.Arrs[i]
					break
				}
			}
			if arrCfg == nil {
				continue
			}

			// Wrap the appropriate client based on kind
			switch arrCfg.Kind {
			case "sonarr":
				if client, ok := sonarrClients[arrID]; ok {
					arrClients[arrID] = &queuecleaner.SonarrClientWrapper{Client: client}
				}
			case "radarr":
				if client, ok := radarrClients[arrID]; ok {
					arrClients[arrID] = &queuecleaner.RadarrClientWrapper{Client: client}
				}
			case "lidarr":
				if client, ok := lidarrClients[arrID]; ok {
					arrClients[arrID] = &queuecleaner.LidarrClientWrapper{Client: client}
				}
			}

			// Also build map of arr configs for removal options
			arrConfigs[arrID] = *arrCfg
		}

		// Use the first bittorrent client
		// TODO: Enhance cleaners to handle multiple bittorrent clients
		qbtClient := qbtClients[bittorrentClientIDs[0]]

		// Get trackers for this cleaner
		trackers := cfg.GetTrackersByIDs(qcConfig.Trackers.TrackerIDs)

		cleaner := queuecleaner.NewCleaner(qcConfig, qbtClient, arrClients, arrConfigs, trackers, logger)
		job := queuecleaner.NewJob(cleaner, qcConfig)

		if err := sched.AddJob(job, qcConfig.Schedule); err != nil {
			logger.Error("Failed to schedule queue cleaner", "cleaner", qcConfig.Name, "error", err)
		}
	}

	// Setup download cleaners
	for _, dcConfig := range cfg.DownloadCleaners {
		if !dcConfig.Enabled {
			continue
		}

		// Get bittorrent clients (use specified ones or all enabled if empty)
		bittorrentClientIDs := dcConfig.Clients.Bittorrent
		if len(bittorrentClientIDs) == 0 {
			// Use all enabled bittorrent clients
			for _, clientCfg := range cfg.BittorrentClients {
				if clientCfg.Enabled {
					bittorrentClientIDs = append(bittorrentClientIDs, clientCfg.ID)
				}
			}
		}
		if len(bittorrentClientIDs) == 0 {
			logger.Warn("Download cleaner requires at least one bittorrent client but none found", "cleaner", dcConfig.Name)
			continue
		}

		// For now, use the first client from each array
		// TODO: Enhance cleaners to handle multiple clients
		qbtClient := qbtClients[bittorrentClientIDs[0]]

		// Get trackers for this cleaner
		trackers := cfg.GetTrackersByIDs(dcConfig.Trackers.TrackerIDs)

		cleaner := downloadcleaner.NewCleaner(dcConfig, qbtClient, trackers, logger)
		job := downloadcleaner.NewJob(cleaner, dcConfig)

		if err := sched.AddJob(job, dcConfig.Schedule); err != nil {
			logger.Error("Failed to schedule download cleaner", "cleaner", dcConfig.Name, "error", err)
		}
	}

	// Setup API server
	// For API, use first enabled clients (or nil if none)
	var defaultQBTClient *qbittorrent.Client
	var defaultSonarrClient *sonarr.Client
	var defaultRadarrClient *radarr.Client
	var defaultLidarrClient *lidarr.Client
	for _, clientCfg := range cfg.BittorrentClients {
		if clientCfg.Enabled && clientCfg.Kind == "qbittorrent" {
			defaultQBTClient = qbtClients[clientCfg.ID]
			break
		}
	}
	for _, arrCfg := range cfg.Arrs {
		if arrCfg.Enabled {
			switch arrCfg.Kind {
			case "sonarr":
				if defaultSonarrClient == nil {
					defaultSonarrClient = sonarrClients[arrCfg.ID]
				}
			case "radarr":
				if defaultRadarrClient == nil {
					defaultRadarrClient = radarrClients[arrCfg.ID]
				}
			case "lidarr":
				if defaultLidarrClient == nil {
					defaultLidarrClient = lidarrClients[arrCfg.ID]
				}
			}
		}
	}

	mux := http.NewServeMux()
	handler := api.NewHandler(cfg, sched, defaultQBTClient, defaultSonarrClient, defaultRadarrClient, defaultLidarrClient, logger, *configPath)
	api.SetupRoutes(mux, handler)

	// Wrap mux with web file handler for unmatched routes
	var httpHandler http.Handler = api.WrapMuxWithWebHandler(mux)

	// Apply middleware
	httpHandler = api.CorsMiddleware()(httpHandler)
	httpHandler = api.LoggingMiddleware(logger)(httpHandler)

	// Start server
	addr := cfg.Server.Host
	if addr == "" {
		addr = "0.0.0.0"
	}
	addr = addr + ":" + fmt.Sprintf("%d", cfg.Server.Port)

	logger.Info("Starting server", "address", addr)

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := http.ListenAndServe(addr, httpHandler); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", "error", err)
			os.Exit(1)
		}
	}()

	// Wait for shutdown signal
	<-sigChan
	logger.Info("Shutting down...")
	sched.Stop()
	logger.Info("Shutdown complete")
}
