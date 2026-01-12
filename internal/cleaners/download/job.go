package download

import "github.com/zombor/purgearr/internal/config"

// Job wraps a Cleaner to implement the scheduler.Job interface
type Job struct {
	cleaner *Cleaner
	config  config.DownloadCleanerConfig
}

// NewJob creates a new download cleaner job
func NewJob(cleaner *Cleaner, cfg config.DownloadCleanerConfig) *Job {
	return &Job{
		cleaner: cleaner,
		config:  cfg,
	}
}

// Run executes the cleaner
func (j *Job) Run() error {
	_, err := j.cleaner.Clean()
	return err
}

// ID returns the job ID
func (j *Job) ID() string {
	return "download-" + j.config.ID
}

// Name returns the job name
func (j *Job) Name() string {
	return j.config.Name
}

