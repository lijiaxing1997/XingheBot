package cron

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func AppendRunRecord(path string, rec RunRecord) error {
	p := strings.TrimSpace(path)
	if p == "" {
		return errors.New("path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	lockPath := p + ".lock"
	return withFileLock(lockPath, 5*time.Second, func() error {
		line, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(append(line, '\n'))
		return err
	})
}
