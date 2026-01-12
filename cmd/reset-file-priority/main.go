package main

import (
	"flag"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/zombor/purgearr/internal/clients/qbittorrent"
	"github.com/zombor/purgearr/internal/config"
)

// getPriorityName returns a human-readable name for the priority value
func getPriorityName(priority int) string {
	switch priority {
	case 0:
		return "Do not download"
	case 1:
		return "Normal"
	case 6:
		return "High"
	case 7:
		return "Maximum"
	default:
		return fmt.Sprintf("Unknown (%d)", priority)
	}
}

func main() {
	dryRun := flag.Bool("dry-run", false, "Show what would be done without making changes")
	debug := flag.Bool("debug", false, "Show debug information about torrents and trackers")
	flag.Parse()

	if *dryRun {
		fmt.Println("DRY RUN MODE - No changes will be made")
		fmt.Println()
	}
	// Load config
	cfg, err := config.Load("config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Find qBittorrent client
	var qbClient *qbittorrent.Client
	for _, client := range cfg.BittorrentClients {
		if client.Kind == "qbittorrent" && client.Enabled {
			qbClient = qbittorrent.NewClient(client.URL, client.Username, client.Password)
			break
		}
	}

	if qbClient == nil {
		log.Fatal("No enabled qBittorrent client found in config")
	}

	// Login
	if err := qbClient.Login(); err != nil {
		log.Fatalf("Failed to login to qBittorrent: %v", err)
	}

	fmt.Println("Connected to qBittorrent successfully")

	// Get all torrents
	torrents, err := qbClient.GetTorrents()
	if err != nil {
		log.Fatalf("Failed to get torrents: %v", err)
	}

	fmt.Printf("Found %d total torrents\n", len(torrents))

	// Debug: Show unique states and tracker samples
	if *debug {
		stateMap := make(map[string]int)
		seedingTrackers := make(map[string]int)
		var seedingSamples []qbittorrent.Torrent

		for _, torrent := range torrents {
			stateMap[torrent.State]++
			// Check if this is a seeding torrent (completed and in seeding state)
			state := strings.ToLower(torrent.State)
			isSeeding := (state == "seeding" || state == "uploading" || state == "stalledup") && torrent.Progress >= 1.0
			if isSeeding {
				seedingTrackers[torrent.Tracker]++
				if len(seedingSamples) < 10 {
					seedingSamples = append(seedingSamples, torrent)
				}
			}
		}

		fmt.Println("\n=== DEBUG INFO ===")
		fmt.Println("Unique torrent states found:")
		for state, count := range stateMap {
			fmt.Printf("  %s: %d\n", state, count)
		}

		fmt.Printf("\nFound %d completed seeding torrents (seeding/uploading/stalledUP with progress >= 100%%)\n", len(seedingTrackers))
		fmt.Println("\nSample of seeding torrents and their trackers:")
		for i, torrent := range seedingSamples {
			if i >= 5 {
				break
			}
			fmt.Printf("  %s: state=%s, progress=%.2f%%, tracker=%s\n", torrent.Name, torrent.State, torrent.Progress*100, torrent.Tracker)
		}

		fmt.Println("\nUnique trackers from seeding torrents:")
		for tracker, count := range seedingTrackers {
			fmt.Printf("  %s: %d torrents\n", tracker, count)
		}
		fmt.Println()
	}

	// Filter for seeding torrents with tracker.tleechreload.org or tracker.torrentleech.org
	// qBittorrent uses "stalledUP" for completed torrents that are seeding but have no peers
	// We also check for "seeding" and "uploading" states, and ensure progress is 100%
	trackerPattern := regexp.MustCompile(`(?i)tracker\.(tleechreload|torrentleech)\.org`)
	var matchingTorrents []qbittorrent.Torrent

	for _, torrent := range torrents {
		// Check if torrent is completed (progress >= 100%)
		if torrent.Progress < 1.0 {
			continue
		}

		// Check if state indicates seeding (stalledUP, seeding, or uploading)
		state := strings.ToLower(torrent.State)
		if state != "seeding" && state != "uploading" && state != "stalledup" {
			continue
		}

		// Check if tracker matches
		if !trackerPattern.MatchString(torrent.Tracker) {
			if *debug {
				fmt.Printf("DEBUG: Skipping torrent %s - state=%s, progress=%.2f%%, tracker=%s (doesn't match pattern)\n", torrent.Name, torrent.State, torrent.Progress*100, torrent.Tracker)
			}
			continue
		}

		matchingTorrents = append(matchingTorrents, torrent)
	}

	fmt.Printf("Found %d seeding torrents from tracker.tleechreload.org or tracker.torrentleech.org\n", len(matchingTorrents))

	if len(matchingTorrents) == 0 {
		fmt.Println("No matching torrents found. Exiting.")
		return
	}

	// Set all files to Normal priority (priority = 1) for each matching torrent
	normalPriority := 1
	successCount := 0
	errorCount := 0

	for _, torrent := range matchingTorrents {
		fmt.Printf("\nProcessing: %s (hash: %s)\n", torrent.Name, torrent.Hash)

		// Get files for this torrent
		files, err := qbClient.GetTorrentFiles(torrent.Hash)
		if err != nil {
			fmt.Printf("  ERROR: Failed to get files: %v\n", err)
			errorCount++
			continue
		}

		fmt.Printf("  Found %d files\n", len(files))

		// Show current file priorities
		if *dryRun {
			fmt.Printf("  Current file priorities:\n")
			for _, file := range files {
				priorityName := getPriorityName(file.Priority)
				fmt.Printf("    - %s (priority: %d - %s)\n", file.Name, file.Priority, priorityName)
			}
		}

		// Collect file indices
		fileIndices := make([]int, 0, len(files))
		for _, file := range files {
			fileIndices = append(fileIndices, file.Index)
		}

		if *dryRun {
			fmt.Printf("  DRY RUN: Would set all %d files to Normal priority (priority: 1)\n", len(files))
			successCount++
		} else {
			// Set all files to Normal priority
			if err := qbClient.SetFilePriority(torrent.Hash, fileIndices, normalPriority); err != nil {
				fmt.Printf("  ERROR: Failed to set file priority: %v\n", err)
				errorCount++
				continue
			}

			fmt.Printf("  SUCCESS: Set all %d files to Normal priority\n", len(files))
			successCount++
		}
	}

	fmt.Printf("\n=== Summary ===\n")
	if *dryRun {
		fmt.Printf("DRY RUN MODE - No changes were made\n")
	}
	fmt.Printf("Total torrents processed: %d\n", len(matchingTorrents))
	if *dryRun {
		fmt.Printf("Would update: %d\n", successCount)
	} else {
		fmt.Printf("Successful: %d\n", successCount)
	}
	fmt.Printf("Errors: %d\n", errorCount)
}
