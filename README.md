# Purgearr

Purgearr is a Go-based application for managing torrents in the *arr ecosystem. It monitors bittorrent clients (starting with qBittorrent) and automatically cleans up torrents based on configurable rules.

## Features

- **Queue Cleaner**: Automatically removes failed, stalled, or slow downloads from the queue
- **Download Cleaner**: Removes completed downloads based on seeding statistics (ratio, seeding time)
- **Multiple Configurations**: Support for multiple cleaner instances with different settings
- **Tracker Filtering**: Filter torrents by tracker URLs (include/exclude modes)
- ***arr Integration**: Connects to Sonarr (with support for Radarr and Lidarr planned)
- **API-First Design**: RESTful API for all operations
- **Web Frontend**: Embedded web UI for monitoring and configuration
- **Single Binary**: Everything packaged in one executable

## Architecture

- **API Server**: Built with Go 1.22+ standard library (`net/http` with `ServeMux`)
- **Clients**: Standard library HTTP clients for Sonarr and qBittorrent APIs
- **Scheduler**: Simple interval-based scheduler for running cleaners
- **Configuration**: YAML-based configuration file

## Installation

1. Clone the repository:
```bash
git clone https://github.com/zombor/purgearr.git
cd purgearr
```

2. Build the binary:
```bash
go build -o purgearr ./cmd/purgearr
```

## Configuration

Copy `config.yaml.example` to `config.yaml` and configure:

- **Server**: Host and port for the API server
- **Sonarr**: URL and API key
- **qBittorrent**: URL, username, and password
- **Queue Cleaners**: Array of queue cleaner configurations
- **Download Cleaners**: Array of download cleaner configurations

### Queue Cleaner Configuration

- `id`: Unique identifier for the cleaner
- `name`: Display name
- `enabled`: Enable/disable the cleaner
- `dry_run`: If true, don't actually delete torrents (just log what would be deleted)
- `schedule`: Schedule interval (e.g., "every 1h", "every 30m")
- `qbittorrent_id`: ID of qBittorrent instance to use (empty = first enabled)
- `sonarr_id`: ID of Sonarr instance to use (empty = first enabled, or none if not set)
- `max_age`: Maximum age for stalled downloads (e.g., "24h")
- `min_speed`: Minimum download speed in bytes/sec
- `remove_failed`: Remove failed torrents
- `remove_stalled`: Remove stalled torrents
- `remove_slow`: Remove slow torrents
- `tracker_filters`: List of tracker URLs to filter
- `filter_mode`: "include" or "exclude" for tracker filters

### Download Cleaner Configuration

- `id`: Unique identifier for the cleaner
- `name`: Display name
- `enabled`: Enable/disable the cleaner
- `dry_run`: If true, don't actually delete torrents (just log what would be deleted)
- `schedule`: Schedule interval (e.g., "every 6h")
- `qbittorrent_id`: ID of qBittorrent instance to use (empty = first enabled)
- `sonarr_id`: ID of Sonarr instance to use (empty = first enabled, or none if not set)
- `max_ratio`: Maximum seeding ratio before removal
- `min_seeding_time`: Minimum seeding time before removal (e.g., "7d")
- `max_seeding_time`: Maximum seeding time before removal (e.g., "30d")
- `tracker_filters`: List of tracker URLs to filter
- `filter_mode`: "include" or "exclude" for tracker filters

## Usage

Run the application:

```bash
./purgearr -config config.yaml
```

The web interface will be available at `http://localhost:8080` (or your configured host/port).

## API Endpoints

- `GET /api/v1/config` - Get current configuration
- `PUT /api/v1/config` - Update configuration
- `GET /api/v1/status` - Get status of all jobs
- `POST /api/v1/cleaners/queue/{id}/run` - Manually run a queue cleaner
- `POST /api/v1/cleaners/download/{id}/run` - Manually run a download cleaner
- `GET /api/v1/clients/sonarr/test` - Test Sonarr connection
- `GET /api/v1/clients/qbittorrent/test` - Test qBittorrent connection
- `GET /api/v1/health` - Health check endpoint

## Development

The project follows Go best practices:

- Standard library preferred (no external routing libraries)
- Proper error handling with wrapped errors
- Clean separation of concerns
- API-first design

## License

[Add your license here]

