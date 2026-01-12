package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/config"
)

func main() {
	completedDir := flag.String("completed-dir", "", "Path to the completed torrent directory (required)")
	qbURL := flag.String("qb-url", "", "qBittorrent URL (optional, uses config.yaml if not provided)")
	qbUsername := flag.String("qb-username", "", "qBittorrent username (optional, uses config.yaml if not provided)")
	qbPassword := flag.String("qb-password", "", "qBittorrent password (optional, uses config.yaml if not provided)")
	qbBasePath := flag.String("qb-base-path", "", "Base path that qBittorrent reports (e.g., /data/completed). If provided, this will be stripped from torrent paths and replaced with the completed-dir path for matching")
	dryRun := flag.Bool("dry-run", false, "Show what would be deleted without actually deleting")
	debug := flag.Bool("debug", false, "Show debug information about path matching")
	flag.Parse()

	if *completedDir == "" {
		log.Fatal("--completed-dir is required")
	}

	// Validate completed directory exists
	info, err := os.Stat(*completedDir)
	if err != nil {
		log.Fatalf("Failed to access completed directory: %v", err)
	}
	if !info.IsDir() {
		log.Fatalf("Completed directory path is not a directory: %s", *completedDir)
	}

	if *dryRun {
		fmt.Println("DRY RUN MODE - No files will be deleted")
		fmt.Println()
	}

	// Initialize qBittorrent client
	var qbClient *qbittorrent.Client
	if *qbURL != "" && *qbUsername != "" && *qbPassword != "" {
		// Use CLI parameters
		qbClient = qbittorrent.NewClient(*qbURL, *qbUsername, *qbPassword)
	} else {
		// Try to load from config
		cfg, err := config.Load("config.yaml")
		if err != nil {
			log.Fatalf("Failed to load config: %v (or provide --qb-url, --qb-username, --qb-password)", err)
		}

		// Find first enabled qBittorrent client
		for _, client := range cfg.BittorrentClients {
			if client.Kind == "qbittorrent" && client.Enabled {
				qbClient = qbittorrent.NewClient(client.URL, client.Username, client.Password)
				break
			}
		}

		if qbClient == nil {
			log.Fatal("No enabled qBittorrent client found in config (or provide --qb-url, --qb-username, --qb-password)")
		}
	}

	// Login to qBittorrent
	if err := qbClient.Login(); err != nil {
		log.Fatalf("Failed to login to qBittorrent: %v", err)
	}

	fmt.Println("Connected to qBittorrent successfully")

	// Normalize completed directory path
	completedDirAbs, err := filepath.Abs(*completedDir)
	if err != nil {
		log.Fatalf("Failed to get absolute path for completed directory: %v", err)
	}
	completedDirAbs = filepath.Clean(completedDirAbs)

	// Resolve symlinks to get the actual path
	completedDirResolved, err := filepath.EvalSymlinks(completedDirAbs)
	if err == nil {
		completedDirAbs = completedDirResolved
	}

	// Normalize qBittorrent base path if provided
	var qbBasePathNormalized string
	if *qbBasePath != "" {
		qbBasePathAbs, err := filepath.Abs(*qbBasePath)
		if err != nil {
			log.Fatalf("Failed to get absolute path for qBittorrent base path: %v", err)
		}
		qbBasePathNormalized = filepath.Clean(qbBasePathAbs)
		if *debug {
			fmt.Printf("Using qBittorrent base path mapping: %s -> %s\n", qbBasePathNormalized, completedDirAbs)
		}
	}

	// Get all torrents
	torrents, err := qbClient.GetTorrents()
	if err != nil {
		log.Fatalf("Failed to get torrents: %v", err)
	}

	fmt.Printf("Found %d torrents in qBittorrent\n", len(torrents))

	// Build a map of relative paths from the completed directory
	// This allows us to compare paths regardless of the base path qBittorrent reports
	torrentRelPaths := make(map[string]bool)
	torrentPathErrors := 0

	fmt.Println("Collecting torrent paths...")
	for i, torrent := range torrents {
		contentPath, err := qbClient.GetTorrentContentPath(torrent.Hash)
		if err != nil {
			fmt.Printf("  Warning: Failed to get content path for torrent %s (%s): %v\n", torrent.Name, torrent.Hash, err)
			torrentPathErrors++
			continue
		}

		// Normalize the path
		contentPath = filepath.Clean(contentPath)

		// Get the relative path from qBittorrent base path (or use the full path if no base path provided)
		var relPath string
		if qbBasePathNormalized != "" && strings.HasPrefix(contentPath, qbBasePathNormalized) {
			// Strip the qBittorrent base path to get relative path
			relPath, err = filepath.Rel(qbBasePathNormalized, contentPath)
			if err != nil {
				// If we can't get relative path, skip this torrent
				continue
			}
		} else {
			// No base path mapping, use the filename as the relative path
			relPath = filepath.Base(contentPath)
		}

		// Normalize the relative path (use forward slashes for consistency)
		relPath = filepath.ToSlash(filepath.Clean(relPath))
		torrentRelPaths[relPath] = true

		// Debug output for specific torrents
		if *debug && (strings.Contains(strings.ToLower(torrent.Name), "xander") || strings.Contains(strings.ToLower(torrent.Name), "xxx")) {
			fmt.Printf("  DEBUG: Torrent '%s' -> relPath: %s\n", torrent.Name, relPath)
		}

		if (i+1)%100 == 0 {
			fmt.Printf("  Processed %d/%d torrents...\n", i+1, len(torrents))
		}
	}

	if torrentPathErrors > 0 {
		fmt.Printf("  Warning: Failed to get content paths for %d torrents\n", torrentPathErrors)
	}

	fmt.Printf("Collected %d unique torrent relative paths\n", len(torrentRelPaths))
	fmt.Println()

	// Scan completed directory (only top-level items, not recursively)
	fmt.Printf("Scanning completed directory: %s\n", completedDirAbs)
	var itemsToDelete []string
	var itemsChecked int

	entries, err := os.ReadDir(completedDirAbs)
	if err != nil {
		log.Fatalf("Error reading completed directory: %v", err)
	}

	fmt.Printf("Found %d top-level items\n", len(entries))

	for i, entry := range entries {
		itemsChecked++

		// Show progress every 100 items
		if (i+1)%100 == 0 {
			fmt.Printf("  Scanned %d/%d items...\n", i+1, len(entries))
		}

		// Skip hidden files (files/directories starting with a period)
		// These are typically qBittorrent .parts files
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}

		// Get relative path from completed directory
		relPath := entry.Name()
		relPathNormalized := filepath.ToSlash(filepath.Clean(relPath))

		// Check if this relative path matches any torrent relative path
		matchesTorrent := torrentRelPaths[relPathNormalized]

		// If not an exact match, check if any torrent path is a parent of this path
		// or if this path is a parent of any torrent path
		if !matchesTorrent {
			for torrentRelPath := range torrentRelPaths {
				// Check if relPathNormalized is a parent of torrentRelPath
				if strings.HasPrefix(torrentRelPath+"/", relPathNormalized+"/") || torrentRelPath == relPathNormalized {
					matchesTorrent = true
					break
				}
				// Check if torrentRelPath is a parent of relPathNormalized
				if strings.HasPrefix(relPathNormalized+"/", torrentRelPath+"/") || torrentRelPath == relPathNormalized {
					matchesTorrent = true
					break
				}
			}
		}

		if *debug && matchesTorrent {
			fmt.Printf("  MATCH: %s\n", relPath)
		}

		if !matchesTorrent {
			if *debug {
				fmt.Printf("  NO MATCH: %s\n", relPath)
			}
			// Get full path for deletion
			fullPath := filepath.Join(completedDirAbs, entry.Name())
			itemsToDelete = append(itemsToDelete, fullPath)
		}
	}

	fmt.Printf("Scanned %d items in completed directory\n", itemsChecked)
	fmt.Printf("Found %d items that don't match any torrent\n", len(itemsToDelete))
	fmt.Println()

	if len(itemsToDelete) == 0 {
		fmt.Println("No orphaned files/directories found. Nothing to delete.")
		return
	}

	// Show what would be deleted
	fmt.Println("Items that would be deleted:")
	for _, item := range itemsToDelete {
		relPath, err := filepath.Rel(completedDirAbs, item)
		if err != nil {
			relPath = item
		}
		fmt.Printf("  - %s\n", relPath)
	}
	fmt.Println()

	if *dryRun {
		fmt.Println("DRY RUN MODE - No files were deleted")
		fmt.Printf("Would delete %d items\n", len(itemsToDelete))
		return
	}

	// Actually delete the items
	fmt.Println("Deleting items...")
	deletedCount := 0
	errorCount := 0

	// Sort items by depth (deepest first) so we delete children before parents
	// This prevents errors when trying to delete a parent directory that still has children
	itemsByDepth := make(map[int][]string)
	maxDepth := 0
	for _, item := range itemsToDelete {
		relPath, _ := filepath.Rel(completedDirAbs, item)
		depth := len(strings.Split(relPath, string(filepath.Separator)))
		itemsByDepth[depth] = append(itemsByDepth[depth], item)
		if depth > maxDepth {
			maxDepth = depth
		}
	}

	// Delete from deepest to shallowest
	for depth := maxDepth; depth >= 1; depth-- {
		for _, item := range itemsByDepth[depth] {
			relPath, err := filepath.Rel(completedDirAbs, item)
			if err != nil {
				relPath = item
			}

			info, err := os.Stat(item)
			if err != nil {
				if os.IsNotExist(err) {
					// Already deleted (might have been a parent directory)
					continue
				}
				fmt.Printf("  ERROR: Failed to stat %s: %v\n", relPath, err)
				errorCount++
				continue
			}

			if info.IsDir() {
				err = os.RemoveAll(item)
			} else {
				err = os.Remove(item)
			}

			if err != nil {
				fmt.Printf("  ERROR: Failed to delete %s: %v\n", relPath, err)
				errorCount++
			} else {
				fmt.Printf("  Deleted: %s\n", relPath)
				deletedCount++
			}
		}
	}

	fmt.Println()
	fmt.Println("=== Summary ===")
	fmt.Printf("Items checked: %d\n", itemsChecked)
	fmt.Printf("Items deleted: %d\n", deletedCount)
	fmt.Printf("Errors: %d\n", errorCount)
}
