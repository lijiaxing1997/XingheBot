package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"test_skill_agent/internal/multiagent"
)

type StoreManager struct {
	JobsPath string
}

func NewStoreManager(jobsPath string) *StoreManager {
	return &StoreManager{JobsPath: strings.TrimSpace(jobsPath)}
}

func (m *StoreManager) Load() (Store, error) {
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return Store{}, errors.New("jobs path is empty")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Store{Version: StoreVersion, Jobs: nil}, nil
		}
		return Store{}, err
	}
	var st Store
	if err := json.Unmarshal(data, &st); err != nil {
		return Store{}, fmt.Errorf("parse jobs store: %w", err)
	}
	if st.Version <= 0 {
		st.Version = StoreVersion
	}
	if st.Jobs == nil {
		st.Jobs = nil
	}
	return st, nil
}

func (m *StoreManager) List() ([]Job, error) {
	st, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := append([]Job(nil), st.Jobs...)
	sort.Slice(out, func(i, j int) bool {
		return strings.TrimSpace(out[i].ID) < strings.TrimSpace(out[j].ID)
	})
	return out, nil
}

func (m *StoreManager) Get(id string) (Job, bool, error) {
	st, err := m.Load()
	if err != nil {
		return Job{}, false, err
	}
	want := strings.TrimSpace(id)
	for _, job := range st.Jobs {
		if strings.TrimSpace(job.ID) == want {
			return job, true, nil
		}
	}
	return Job{}, false, nil
}

func (m *StoreManager) Upsert(job Job, now time.Time, defaultTimezone string, minRefireGap time.Duration) (Job, error) {
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return Job{}, errors.New("jobs path is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	lockPath := path + ".lock"
	var out Job
	if err := withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		st.Version = StoreVersion

		updated := normalizeJob(job, now)
		if updated.Schedule.Timezone == "" {
			updated.Schedule.Timezone = strings.TrimSpace(defaultTimezone)
		}
		next, nextErr := ComputeNextRunAt(updated, now, defaultTimezone, minRefireGap)
		if nextErr != nil {
			return nextErr
		}
		if updated.Enabled {
			updated.NextRunAt = next
		} else {
			updated.NextRunAt = time.Time{}
		}

		replaced := false
		for i := range st.Jobs {
			if strings.TrimSpace(st.Jobs[i].ID) != strings.TrimSpace(updated.ID) {
				continue
			}
			if st.Jobs[i].CreatedAt.IsZero() {
				st.Jobs[i].CreatedAt = updated.CreatedAt
			}
			updated.CreatedAt = st.Jobs[i].CreatedAt
			st.Jobs[i] = updated
			replaced = true
			break
		}
		if !replaced {
			st.Jobs = append(st.Jobs, updated)
		}
		if err := writeJSONAtomic(path, st); err != nil {
			return err
		}
		out = updated
		return nil
	}); err != nil {
		return Job{}, err
	}
	return out, nil
}

func (m *StoreManager) Delete(id string, now time.Time) error {
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return errors.New("jobs path is empty")
	}
	lockPath := path + ".lock"
	return withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		want := strings.TrimSpace(id)
		if want == "" {
			return errors.New("id is required")
		}
		kept := st.Jobs[:0]
		for _, job := range st.Jobs {
			if strings.TrimSpace(job.ID) == want {
				continue
			}
			kept = append(kept, job)
		}
		st.Jobs = kept
		st.Version = StoreVersion
		return writeJSONAtomic(path, st)
	})
}

func (m *StoreManager) SetEnabled(id string, enabled bool, now time.Time, defaultTimezone string, minRefireGap time.Duration) (Job, error) {
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return Job{}, errors.New("jobs path is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	lockPath := path + ".lock"
	var out Job
	if err := withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		want := strings.TrimSpace(id)
		if want == "" {
			return errors.New("id is required")
		}
		found := false
		for i := range st.Jobs {
			if strings.TrimSpace(st.Jobs[i].ID) != want {
				continue
			}
			st.Jobs[i].Enabled = enabled
			st.Jobs[i].UpdatedAt = now
			if enabled {
				next, nextErr := ComputeNextRunAt(st.Jobs[i], now, defaultTimezone, minRefireGap)
				if nextErr != nil {
					return nextErr
				}
				st.Jobs[i].NextRunAt = next
				st.Jobs[i].BackoffUntil = time.Time{}
			} else {
				st.Jobs[i].NextRunAt = time.Time{}
				st.Jobs[i].RunningAt = time.Time{}
			}
			out = st.Jobs[i]
			found = true
			break
		}
		if !found {
			return fmt.Errorf("job not found: %s", want)
		}
		st.Version = StoreVersion
		return writeJSONAtomic(path, st)
	}); err != nil {
		return Job{}, err
	}
	return out, nil
}

func (m *StoreManager) RunNow(id string, now time.Time) (Job, error) {
	path := strings.TrimSpace(m.JobsPath)
	if path == "" {
		return Job{}, errors.New("jobs path is empty")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()

	lockPath := path + ".lock"
	var out Job
	if err := withFileLock(lockPath, 5*time.Second, func() error {
		st, err := m.Load()
		if err != nil {
			return err
		}
		want := strings.TrimSpace(id)
		if want == "" {
			return errors.New("id is required")
		}
		found := false
		for i := range st.Jobs {
			if strings.TrimSpace(st.Jobs[i].ID) != want {
				continue
			}
			st.Jobs[i].Enabled = true
			st.Jobs[i].UpdatedAt = now
			st.Jobs[i].NextRunAt = now
			st.Jobs[i].BackoffUntil = time.Time{}
			st.Jobs[i].RunningAt = time.Time{}
			out = st.Jobs[i]
			found = true
			break
		}
		if !found {
			return fmt.Errorf("job not found: %s", want)
		}
		st.Version = StoreVersion
		return writeJSONAtomic(path, st)
	}); err != nil {
		return Job{}, err
	}
	return out, nil
}

func normalizeJob(job Job, now time.Time) Job {
	out := job
	if strings.TrimSpace(out.ID) == "" {
		out.ID = GenerateJobID()
	}
	out.ID = multiagent.SanitizeID(out.ID, GenerateJobID())
	out.Name = strings.TrimSpace(out.Name)
	if out.Name == "" {
		out.Name = out.ID
	}
	out.Schedule.Type = strings.ToLower(strings.TrimSpace(out.Schedule.Type))
	if out.Schedule.Type == "" {
		out.Schedule.Type = "cron"
	}
	out.Schedule.Expr = strings.TrimSpace(out.Schedule.Expr)
	out.Schedule.Every = strings.TrimSpace(out.Schedule.Every)
	out.Schedule.At = strings.TrimSpace(out.Schedule.At)
	out.Schedule.Timezone = strings.TrimSpace(out.Schedule.Timezone)
	out.Task.Type = strings.ToLower(strings.TrimSpace(out.Task.Type))
	if out.Task.Type == "" {
		out.Task.Type = "llm"
	}
	out.Task.Prompt = strings.TrimSpace(out.Task.Prompt)
	if out.Task.MaxTurns < 0 {
		out.Task.MaxTurns = 0
	}
	out.Delivery.Type = strings.ToLower(strings.TrimSpace(out.Delivery.Type))
	if out.Delivery.Type == "" {
		out.Delivery.Type = "email"
	}
	out.Delivery.Subject = strings.TrimSpace(out.Delivery.Subject)
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = now.UTC()
	if out.CreatedAt.IsZero() {
		out.CreatedAt = now.UTC()
	}
	if out.FailCount < 0 {
		out.FailCount = 0
	}
	out.LastError = strings.TrimSpace(out.LastError)
	return out
}

func GenerateJobID() string {
	return "job-" + time.Now().UTC().Format("20060102-150405") + "-" + randomHex(3)
}

func randomHex(n int) string {
	if n <= 0 {
		n = 4
	}
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		now := time.Now().UTC().UnixNano()
		return fmt.Sprintf("%d", now)
	}
	return hex.EncodeToString(buf)
}

func writeJSONAtomic(path string, payload any) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp-%d", path, time.Now().UTC().UnixNano())
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func withFileLock(lockPath string, timeout time.Duration, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	start := time.Now().UTC()
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_ = f.Close()
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if timeout > 0 && time.Since(start) > timeout {
			return fmt.Errorf("acquire lock timeout: %s", lockPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
	defer os.Remove(lockPath)
	return fn()
}
