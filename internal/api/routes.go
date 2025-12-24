package api

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var webFiles embed.FS

// SetupRoutes configures all API routes
func SetupRoutes(mux *http.ServeMux, handler *Handler) {
	// API routes
	mux.HandleFunc("GET /api/v1/config", handler.GetConfig)
	mux.HandleFunc("PUT /api/v1/config", handler.UpdateConfig)
	mux.HandleFunc("GET /api/v1/status", handler.GetStatus)
	mux.HandleFunc("POST /api/v1/cleaners/queue/{id}/run", handler.RunQueueCleaner)
	mux.HandleFunc("POST /api/v1/cleaners/download/{id}/run", handler.RunDownloadCleaner)
	mux.HandleFunc("GET /api/v1/clients/sonarr/test", handler.TestSonarrConnection)
	mux.HandleFunc("GET /api/v1/clients/qbittorrent/test", handler.TestQBittorrentConnection)
	mux.HandleFunc("GET /api/v1/health", handler.HealthCheck)

	// Serve web frontend
	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	
	fileServer := http.FileServer(http.FS(webFS))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		// Serve index.html for root path
		if r.URL.Path == "/" {
			r.URL.Path = "/index.html"
		}
		// Don't serve files for API paths
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			http.NotFound(w, r)
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

