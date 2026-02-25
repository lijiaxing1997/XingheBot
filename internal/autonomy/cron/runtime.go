package cron

import (
	"errors"
	"strings"
	"time"
)

type ClaimOptions struct {
	DefaultTimezone string
	StuckRun        time.Duration
	MinRefireGap    time.Duration
}

func (m *StoreManager) ClaimDueJobs(now time.Time, opts ClaimOptions) (due []Job, next time.Time, err error) {
	if m == nil {
		return nil, time.Time{}, errors.New("store manager is nil")
	}
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return nil, time.Time{}, errors.New("jobs path is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	lockPath := path + ".lock"

	var mutated bool
	due = nil
	next = time.Time{}

	err = withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		if st.Version <= 0 {
			st.Version = StoreVersion
			mutated = true
		}
		for i := range st.Jobs {
			job := st.Jobs[i]
			if !job.Enabled {
				continue
			}

			if !job.RunningAt.IsZero() && opts.StuckRun > 0 && now.Sub(job.RunningAt) > opts.StuckRun {
				job.FailCount++
				job.LastError = "job stuck; cleared running flag"
				job.RunningAt = time.Time{}
				job.BackoffUntil = now.Add(backoffDuration(job.FailCount))
				job.NextRunAt = job.BackoffUntil
				job.UpdatedAt = now
				st.Jobs[i] = job
				mutated = true
			}

			if job.NextRunAt.IsZero() {
				nxt, err := ComputeNextRunAt(job, now, opts.DefaultTimezone, opts.MinRefireGap)
				if err != nil {
					job.FailCount++
					job.LastError = err.Error()
					job.BackoffUntil = now.Add(backoffDuration(job.FailCount))
					job.NextRunAt = job.BackoffUntil
					job.UpdatedAt = now
					st.Jobs[i] = job
					mutated = true
				} else {
					job.NextRunAt = nxt
					job.UpdatedAt = now
					st.Jobs[i] = job
					mutated = true
				}
			}

			if !job.BackoffUntil.IsZero() && job.BackoffUntil.After(now) {
				if next.IsZero() || job.BackoffUntil.Before(next) {
					next = job.BackoffUntil
				}
				continue
			}

			if !job.RunningAt.IsZero() {
				continue
			}

			if !job.NextRunAt.IsZero() && !job.NextRunAt.After(now) {
				job.RunningAt = now
				job.UpdatedAt = now
				st.Jobs[i] = job
				due = append(due, job)
				mutated = true
				continue
			}

			if !job.NextRunAt.IsZero() {
				if next.IsZero() || job.NextRunAt.Before(next) {
					next = job.NextRunAt
				}
			}
		}

		if mutated {
			st.Version = StoreVersion
			return writeJSONAtomic(path, st)
		}
		return nil
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	return due, next, nil
}

type FinishOptions struct {
	DefaultTimezone string
	MinRefireGap    time.Duration
}

func (m *StoreManager) FinishJob(id string, claimedAt time.Time, finishedAt time.Time, runErr error, opts FinishOptions) error {
	if m == nil {
		return errors.New("store manager is nil")
	}
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return errors.New("jobs path is empty")
	}
	want := strings.TrimSpace(id)
	if want == "" {
		return errors.New("id is required")
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}
	finishedAt = finishedAt.UTC()

	lockPath := path + ".lock"
	return withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		for i := range st.Jobs {
			job := st.Jobs[i]
			if strings.TrimSpace(job.ID) != want {
				continue
			}
			if !job.RunningAt.IsZero() && !claimedAt.IsZero() && !job.RunningAt.Equal(claimedAt.UTC()) {
				// Another process claimed it.
				return nil
			}
			job.RunningAt = time.Time{}
			job.UpdatedAt = finishedAt
			if runErr == nil {
				job.LastRunAt = finishedAt
				job.FailCount = 0
				job.LastError = ""
				job.BackoffUntil = time.Time{}
				if strings.EqualFold(strings.TrimSpace(job.Schedule.Type), "at") {
					job.Enabled = false
					job.NextRunAt = time.Time{}
				} else if job.Enabled {
					nxt, err := ComputeNextRunAt(job, finishedAt, opts.DefaultTimezone, opts.MinRefireGap)
					if err != nil {
						job.FailCount++
						job.LastError = err.Error()
						job.BackoffUntil = finishedAt.Add(backoffDuration(job.FailCount))
						job.NextRunAt = job.BackoffUntil
					} else {
						job.NextRunAt = nxt
					}
				}
			} else {
				job.FailCount++
				job.LastError = strings.TrimSpace(runErr.Error())
				job.BackoffUntil = finishedAt.Add(backoffDuration(job.FailCount))
				job.NextRunAt = job.BackoffUntil
			}
			st.Jobs[i] = job
			st.Version = StoreVersion
			return writeJSONAtomic(path, st)
		}
		return nil
	})
}

func backoffDuration(fails int) time.Duration {
	switch {
	case fails <= 0:
		return 30 * time.Second
	case fails == 1:
		return 30 * time.Second
	case fails == 2:
		return 1 * time.Minute
	case fails == 3:
		return 5 * time.Minute
	case fails == 4:
		return 15 * time.Minute
	default:
		return 60 * time.Minute
	}
}
