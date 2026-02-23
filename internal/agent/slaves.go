package agent

import "time"

type SlaveSummary struct {
	SlaveID   string
	Name      string
	Status    string
	LastSeen  time.Time
}

type SlaveListProvider interface {
	SnapshotSlaves() []SlaveSummary
}

