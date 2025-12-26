# Purgearr

Purgearr is a Go-based application for managing torrents in the *arr ecosystem. It monitors bittorrent clients and automatically cleans up torrents based on configurable rules.

## Features

- **Queue Cleaner**: Automatically removes failed, stalled, or slow downloads from the queue
- **Download Cleaner**: Removes completed downloads based on seeding statistics (ratio, seeding time)
- **Malware Blocker**: Automatically blocks files matching regex patterns (e.g., samples, trailers, executables) from downloading
- **Multiple Configurations**: Support for multiple cleaner instances with different settings
- **Tracker Filtering**: Filter torrents by tracker URLs using regex patterns (include/exclude modes)
- **Multiple Clients**: Support for multiple Sonarr and qBittorrent instances
- ***arr Integration**: Connects to Sonarr (with support for Radarr and Lidarr planned)
- **API-First Design**: RESTful API for all operations
- **Web Frontend**: Embedded web UI for monitoring, configuration, and manual execution
- **Dry Run Mode**: Test cleaners without actually deleting torrents
- **Structured Logging**: Uses Go's `slog` for better log parsing and filtering
- **Single Binary**: Everything packaged in one executable

## Architecture

- **API Server**: Built with Go 1.22+ standard library (`net/http` with `ServeMux`)
- **Clients**: Standard library HTTP clients for Sonarr and qBittorrent APIs
- **Scheduler**: Simple interval-based scheduler for running cleaners and malware blocker
- **Configuration**: YAML-based configuration file with embedded web frontend

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

Copy `config.yaml.example` to `config.yaml` and configure your settings. The example file includes detailed comments for all configuration options.

Configuration is based around the following ideas:

- A list of `arr` apps
- A list of bittorrent clients
- A list of trackers

These lists are used in cleaners. Each cleaner kind supports binding to any/all of the above lists. e.g. you can have multiple queue cleaners for different kinds of arr apps, clients and trackers.

This lets you have different cleaners specific to arr apps and different trackers for those apps. e.g. different trackers might have different seeding rules and this lets you configure the cleaner to account for this.

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
- `POST /api/v1/cleaners/malware-blocker/run` - Manually run the malware blocker
- `GET /api/v1/clients/sonarr/test` - Test Sonarr connection
- `GET /api/v1/clients/qbittorrent/test` - Test qBittorrent connection
- `GET /api/v1/health` - Health check endpoint

## Web Interface

The web interface provides:
- Status dashboard showing all jobs and their schedules
- Manual execution of individual cleaners
- Client connection testing
- Real-time job status updates

## License

MIT License