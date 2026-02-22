package multiagent

import (
	"errors"
	"os"
	"strings"
	"time"
)

const runTitleMetadataKey = "title"

func (c *Coordinator) UpdateRun(runID string, updateFn func(*RunManifest) error) (RunManifest, error) {
	if c == nil {
		return RunManifest{}, errors.New("coordinator is nil")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return RunManifest{}, errors.New("run_id is required")
	}
	id = SanitizeID(id, GenerateRunID())

	path := c.RunManifestPath(id)
	lockPath := path + ".lock"

	var updated RunManifest
	err := withFileLock(lockPath, 2*time.Second, func() error {
		run, err := c.EnsureRun(id)
		if err != nil {
			return err
		}
		if run.Metadata == nil {
			run.Metadata = make(map[string]any)
		}
		if updateFn != nil {
			if err := updateFn(&run); err != nil {
				return err
			}
		}
		if len(run.Metadata) == 0 {
			run.Metadata = nil
		}
		if err := writeJSONAtomic(path, run); err != nil {
			return err
		}
		updated = run
		return nil
	})
	if err != nil {
		return RunManifest{}, err
	}
	return updated, nil
}

func (c *Coordinator) SetRunTitle(runID string, title string) (RunManifest, error) {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return RunManifest{}, errors.New("title is required")
	}
	if len([]rune(trimmed)) > 80 {
		trimmed = string([]rune(trimmed)[:80])
	}
	return c.UpdateRun(runID, func(run *RunManifest) error {
		if run == nil {
			return nil
		}
		if run.Metadata == nil {
			run.Metadata = make(map[string]any)
		}
		run.Metadata[runTitleMetadataKey] = trimmed
		return nil
	})
}

func RunTitle(run RunManifest) string {
	if run.Metadata != nil {
		if v, ok := run.Metadata[runTitleMetadataKey]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			}
		}
	}
	if strings.TrimSpace(run.ID) != "" {
		return strings.TrimSpace(run.ID)
	}
	return "untitled"
}

func (c *Coordinator) DeleteRun(runID string) error {
	if c == nil {
		return errors.New("coordinator is nil")
	}
	id := strings.TrimSpace(runID)
	if id == "" {
		return errors.New("run_id is required")
	}
	id = SanitizeID(id, GenerateRunID())
	return os.RemoveAll(c.RunDir(id))
}

