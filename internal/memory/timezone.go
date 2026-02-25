package memory

import (
	"strings"
	"time"
)

func ResolveLocation(name string) (*time.Location, error) {
	raw := strings.TrimSpace(name)
	if raw == "" {
		return time.Local, nil
	}
	switch strings.ToLower(raw) {
	case "local":
		return time.Local, nil
	case "utc":
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(raw)
	if err != nil {
		return time.UTC, err
	}
	return loc, nil
}
