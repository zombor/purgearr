package scheduler

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// Job represents a scheduled job
type Job interface {
	Run() error
	ID() string
	Name() string
}

// Scheduler manages scheduled jobs
type Scheduler struct {
	jobs  map[string]*scheduledJob
	stop  chan struct{}
	logger *slog.Logger
}

type scheduledJob struct {
	job      Job
	schedule string
	ticker   *time.Ticker
	duration time.Duration
	stop     chan struct{}
	lastRun  time.Time
	nextRun  time.Time
}

// NewScheduler creates a new scheduler
func NewScheduler(logger *slog.Logger) *Scheduler {
	return &Scheduler{
		jobs:   make(map[string]*scheduledJob),
		stop:   make(chan struct{}),
		logger: logger,
	}
}

// AddJob adds a job to the scheduler with a cron-like schedule
// Schedule format: "every X" where X is a duration (e.g., "every 1h", "every 30m")
// For now, we support simple interval-based scheduling
func (s *Scheduler) AddJob(job Job, schedule string) error {
	duration, err := parseSchedule(schedule)
	if err != nil {
		return fmt.Errorf("parsing schedule '%s': %w", schedule, err)
	}

	ticker := time.NewTicker(duration)
	sj := &scheduledJob{
		job:      job,
		schedule: schedule,
		ticker:   ticker,
		duration: duration,
		stop:     make(chan struct{}),
		nextRun:  time.Now().Add(duration),
	}

	s.jobs[job.ID()] = sj

	// Start the job goroutine
	go s.runJob(sj)

	s.logger.Info("Scheduled job", "name", job.Name(), "id", job.ID(), "schedule", duration)
	return nil
}

// RemoveJob removes a job from the scheduler
func (s *Scheduler) RemoveJob(id string) {
	if sj, ok := s.jobs[id]; ok {
		close(sj.stop)
		sj.ticker.Stop()
		delete(s.jobs, id)
		s.logger.Info("Removed scheduled job", "id", id)
	}
}

// RunJobNow runs a job immediately
func (s *Scheduler) RunJobNow(id string) error {
	sj, ok := s.jobs[id]
	if !ok {
		return fmt.Errorf("job not found: %s", id)
	}

	s.logger.Info("Running job manually", "name", sj.job.Name(), "id", sj.job.ID())
	if err := sj.job.Run(); err != nil {
		return fmt.Errorf("running job: %w", err)
	}

	sj.lastRun = time.Now()
	return nil
}

// GetJobStatus returns the status of a job
func (s *Scheduler) GetJobStatus(id string) (map[string]interface{}, error) {
	sj, ok := s.jobs[id]
	if !ok {
		return nil, fmt.Errorf("job not found: %s", id)
	}

	return map[string]interface{}{
		"id":        sj.job.ID(),
		"name":      sj.job.Name(),
		"schedule":  sj.schedule,
		"last_run":  sj.lastRun,
		"next_run":  sj.nextRun,
	}, nil
}

// GetAllJobs returns all scheduled jobs
func (s *Scheduler) GetAllJobs() map[string]map[string]interface{} {
	result := make(map[string]map[string]interface{})
	for id, sj := range s.jobs {
		result[id] = map[string]interface{}{
			"id":        sj.job.ID(),
			"name":      sj.job.Name(),
			"schedule":  sj.schedule,
			"last_run":  sj.lastRun,
			"next_run":  sj.nextRun,
		}
	}
	return result
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	close(s.stop)
	for _, sj := range s.jobs {
		close(sj.stop)
		sj.ticker.Stop()
	}
	s.logger.Info("Scheduler stopped")
}

func (s *Scheduler) runJob(sj *scheduledJob) {
	// Run immediately on start
	s.logger.Info("Running job", "name", sj.job.Name(), "id", sj.job.ID())
	if err := sj.job.Run(); err != nil {
		s.logger.Error("Error running job", "id", sj.job.ID(), "error", err)
	}
	sj.lastRun = time.Now()
	sj.nextRun = time.Now().Add(sj.duration)

	for {
		select {
		case <-sj.ticker.C:
			s.logger.Info("Running scheduled job", "name", sj.job.Name(), "id", sj.job.ID())
			if err := sj.job.Run(); err != nil {
				s.logger.Error("Error running job", "id", sj.job.ID(), "error", err)
			}
			sj.lastRun = time.Now()
			sj.nextRun = time.Now().Add(sj.duration)
		case <-sj.stop:
			return
		case <-s.stop:
			return
		}
	}
}

// parseSchedule parses a schedule string into a duration
// Supports formats like "every 1h", "every 30m", "every 5m", etc.
func parseSchedule(schedule string) (time.Duration, error) {
	schedule = strings.TrimSpace(schedule)
	schedule = strings.ToLower(schedule)

	if !strings.HasPrefix(schedule, "every ") {
		return 0, fmt.Errorf("schedule must start with 'every '")
	}

	durationStr := strings.TrimPrefix(schedule, "every ")
	duration, err := time.ParseDuration(durationStr)
	if err != nil {
		return 0, fmt.Errorf("invalid duration: %w", err)
	}

	if duration < time.Minute {
		return 0, fmt.Errorf("schedule must be at least 1 minute")
	}

	return duration, nil
}

