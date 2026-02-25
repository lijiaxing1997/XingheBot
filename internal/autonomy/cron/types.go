package cron

import "time"

const StoreVersion = 1

type Store struct {
	Version int   `json:"version"`
	Jobs    []Job `json:"jobs"`
}

type Job struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`

	Schedule Schedule `json:"schedule"`
	Task     Task     `json:"task"`
	Delivery Delivery `json:"delivery"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	NextRunAt    time.Time `json:"next_run_at,omitempty"`
	LastRunAt    time.Time `json:"last_run_at,omitempty"`
	RunningAt    time.Time `json:"running_at,omitempty"`
	BackoffUntil time.Time `json:"backoff_until,omitempty"`
	FailCount    int       `json:"fail_count,omitempty"`
	LastError    string    `json:"last_error,omitempty"`
}

type Schedule struct {
	Type     string `json:"type"` // cron|every|at
	Expr     string `json:"expr,omitempty"`
	Every    string `json:"every,omitempty"`
	At       string `json:"at,omitempty"` // RFC3339 (default: local if no TZ)
	Timezone string `json:"timezone,omitempty"`
}

type Task struct {
	Type     string `json:"type"` // llm
	Prompt   string `json:"prompt"`
	MaxTurns int    `json:"max_turns,omitempty"`
}

type Delivery struct {
	Type    string   `json:"type"` // email
	To      []string `json:"to,omitempty"`
	Subject string   `json:"subject,omitempty"`
}

type RunRecord struct {
	JobID         string    `json:"job_id"`
	StartedAt     time.Time `json:"started_at"`
	FinishedAt    time.Time `json:"finished_at"`
	Status        string    `json:"status"` // ok|error|skipped
	Error         string    `json:"error,omitempty"`
	Delivered     bool      `json:"delivered"`
	DeliveryErr   string    `json:"delivery_error,omitempty"`
	OutputPreview string    `json:"output_preview,omitempty"`
	OutputFile    string    `json:"output_file,omitempty"` // relative to runs dir
}
