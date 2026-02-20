package multiagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
)

func TailFileLines(path string, maxLines int, maxBytes int) ([]string, error) {
	if maxLines <= 0 {
		maxLines = 50
	}
	if maxBytes <= 0 {
		maxBytes = 128 * 1024
	}

	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	size := info.Size()
	var offset int64
	if size > int64(maxBytes) {
		offset = size - int64(maxBytes)
	}
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		// Drop the first partial line to avoid showing truncated content.
		if idx := bytes.IndexByte(data, '\n'); idx >= 0 {
			data = data[idx+1:]
		}
	}

	if len(data) == 0 {
		return nil, nil
	}

	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) <= maxLines {
		return lines, nil
	}
	return lines[len(lines)-maxLines:], nil
}

func TailFileText(path string, maxLines int, maxBytes int) (string, error) {
	lines, err := TailFileLines(path, maxLines, maxBytes)
	if err != nil || len(lines) == 0 {
		return "", err
	}
	return strings.Join(lines, "\n"), nil
}

func TailJSONL[T any](path string, maxLines int, maxBytes int) ([]T, error) {
	lines, err := TailFileLines(path, maxLines, maxBytes)
	if err != nil || len(lines) == 0 {
		return nil, err
	}

	out := make([]T, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item T
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

