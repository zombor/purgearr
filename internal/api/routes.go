package api

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
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
	mux.HandleFunc("POST /api/v1/cleaners/malware-blocker/run", handler.RunMalwareBlocker)
	mux.HandleFunc("GET /api/v1/clients/sonarr/test", handler.TestSonarrConnection)
	mux.HandleFunc("GET /api/v1/clients/qbittorrent/test", handler.TestQBittorrentConnection)
	mux.HandleFunc("GET /api/v1/health", handler.HealthCheck)

	// Web routes are handled by WrapMuxWithWebHandler
}

// WrapMuxWithWebHandler wraps the mux with a handler that serves web files for unmatched routes
func WrapMuxWithWebHandler(mux *http.ServeMux) http.Handler {
	webFS, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err)
	}
	
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API paths go to mux
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		
		// For GET requests to non-API paths, serve web files
		if r.Method == "GET" {
			// Handle root path - serve index.html
			if r.URL.Path == "/" {
				serveFile(w, r, webFS, "index.html")
				return
			}
			
			// Get path from URL
			path := strings.TrimPrefix(r.URL.Path, "/")
			path = filepath.Clean(path)
			if path == "." || path == "/" {
				path = ""
			}
			
			serveFile(w, r, webFS, path)
			return
		}
		
		// For non-GET requests, use mux (should be API routes)
		mux.ServeHTTP(w, r)
	})
}

// serveFile serves a file from the embedded filesystem
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS, filePath string) {
	file, err := fsys.Open(filePath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	
	// Set content type based on file extension
	ext := filepath.Ext(filePath)
	switch ext {
	case ".html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	case ".css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case ".js":
		w.Header().Set("Content-Type", "application/javascript")
	case ".json":
		w.Header().Set("Content-Type", "application/json")
	case ".png":
		w.Header().Set("Content-Type", "image/png")
	case ".jpg", ".jpeg":
		w.Header().Set("Content-Type", "image/jpeg")
	case ".svg":
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	
	// Copy file content to response
	io.Copy(w, file)
}

