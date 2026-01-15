package queue

import "github.com/zombor/purgearr/internal/config"

// Job wraps a Cleaner to implement the scheduler.Job interface
type Job struct {
	cleaner *Cleaner
	config  config.QueueCleanerConfig
}

// NewJob creates a new queue cleaner job
func NewJob(cleaner *Cleaner, cfg config.QueueCleanerConfig) *Job {
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
	return "queue-" + j.config.ID
}

// Name returns the job name
func (j *Job) Name() string {
	return j.config.Name
}

// GetCleaner returns the underlying cleaner
func (j *Job) GetCleaner() *Cleaner {
	return j.cleaner
}

