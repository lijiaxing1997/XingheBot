package multiagent

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
)

func (c *Coordinator) ListRuns() ([]RunManifest, error) {
	if c == nil {
		return nil, errors.New("coordinator is nil")
	}
	entries, err := os.ReadDir(c.RunRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	out := make([]RunManifest, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		path := filepath.Join(c.RunRoot, id, "run.json")
		var run RunManifest
		if err := readJSONFile(path, &run); err != nil {
			continue
		}
		if run.ID == "" {
			run.ID = id
		}
		out = append(out, run)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		if out[i].CreatedAt.IsZero() {
			return false
		}
		if out[j].CreatedAt.IsZero() {
			return true
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})

	return out, nil
}

