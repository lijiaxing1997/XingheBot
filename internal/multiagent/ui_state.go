package multiagent

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	runUIStateVersion  = 2
	runUIStateFileName = "ui_state.json"
)

type HiddenAgentRecord struct {
	AgentID  string    `json:"agent_id"`
	HiddenAt time.Time `json:"hidden_at"`
	Reason   string    `json:"reason,omitempty"`
}

type ReportedAgentResultRecord struct {
	AgentID      string    `json:"agent_id"`
	Status       string    `json:"status,omitempty"`
	FinishedAt   time.Time `json:"finished_at,omitempty"`
	ReportedAt   time.Time `json:"reported_at,omitempty"`
	ResultPath   string    `json:"result_path,omitempty"`
	Error        string    `json:"error,omitempty"`
	PreviewChars int       `json:"preview_chars,omitempty"`
}

type RunUIState struct {
	Version              int                                  `json:"version"`
	HiddenAgents         map[string]HiddenAgentRecord         `json:"hidden_agents,omitempty"`
	ReportedAgentResults map[string]ReportedAgentResultRecord `json:"reported_agent_results,omitempty"`
	UpdatedAt            time.Time                            `json:"updated_at,omitempty"`
}

func (c *Coordinator) RunUIStatePath(runID string) string {
	if c == nil {
		return ""
	}
	return filepath.Join(c.RunDir(runID), runUIStateFileName)
}

func (c *Coordinator) ReadRunUIState(runID string) (RunUIState, error) {
	if c == nil {
		return RunUIState{}, errors.New("coordinator is nil")
	}
	if strings.TrimSpace(runID) == "" {
		return RunUIState{}, errors.New("run_id is required")
	}

	var out RunUIState
	if err := readJSONFile(c.RunUIStatePath(runID), &out); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RunUIState{Version: runUIStateVersion}, nil
		}
		return RunUIState{}, err
	}
	if out.Version <= 0 {
		out.Version = runUIStateVersion
	}
	if out.HiddenAgents == nil {
		out.HiddenAgents = make(map[string]HiddenAgentRecord)
	}
	if out.ReportedAgentResults == nil {
		out.ReportedAgentResults = make(map[string]ReportedAgentResultRecord)
	}
	return out, nil
}

func (c *Coordinator) SetAgentsHidden(runID string, agentIDs []string, hidden bool, reason string, now time.Time) (RunUIState, error) {
	if c == nil {
		return RunUIState{}, errors.New("coordinator is nil")
	}
	if strings.TrimSpace(runID) == "" {
		return RunUIState{}, errors.New("run_id is required")
	}
	if len(agentIDs) == 0 {
		return c.ReadRunUIState(runID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	path := c.RunUIStatePath(runID)
	lockPath := path + ".lock"

	var updated RunUIState
	err := withFileLock(lockPath, 2*time.Second, func() error {
		current, err := c.ReadRunUIState(runID)
		if err != nil {
			return err
		}
		if current.HiddenAgents == nil {
			current.HiddenAgents = make(map[string]HiddenAgentRecord)
		}
		trimReason := strings.TrimSpace(reason)

		for _, raw := range agentIDs {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if hidden {
				rec := current.HiddenAgents[id]
				if strings.TrimSpace(rec.AgentID) == "" {
					rec.AgentID = id
				}
				if rec.HiddenAt.IsZero() {
					rec.HiddenAt = now
				}
				if trimReason != "" {
					rec.Reason = trimReason
				}
				current.HiddenAgents[id] = rec
				continue
			}
			delete(current.HiddenAgents, id)
		}

		current.Version = runUIStateVersion
		current.UpdatedAt = now
		if len(current.HiddenAgents) == 0 {
			current.HiddenAgents = nil
		}
		if len(current.ReportedAgentResults) == 0 {
			current.ReportedAgentResults = nil
		}
		if err := writeJSONAtomic(path, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	if err != nil {
		return RunUIState{}, err
	}
	return updated, nil
}

func (c *Coordinator) MarkAgentResultsReported(runID string, reports []ReportedAgentResultRecord, now time.Time) (RunUIState, error) {
	if c == nil {
		return RunUIState{}, errors.New("coordinator is nil")
	}
	if strings.TrimSpace(runID) == "" {
		return RunUIState{}, errors.New("run_id is required")
	}
	if len(reports) == 0 {
		return c.ReadRunUIState(runID)
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	path := c.RunUIStatePath(runID)
	lockPath := path + ".lock"

	var updated RunUIState
	err := withFileLock(lockPath, 2*time.Second, func() error {
		current, err := c.ReadRunUIState(runID)
		if err != nil {
			return err
		}
		if current.ReportedAgentResults == nil {
			current.ReportedAgentResults = make(map[string]ReportedAgentResultRecord)
		}

		for _, report := range reports {
			id := strings.TrimSpace(report.AgentID)
			if id == "" {
				continue
			}
			existing := current.ReportedAgentResults[id]
			if strings.TrimSpace(existing.AgentID) == "" {
				existing.AgentID = id
			}
			if !report.FinishedAt.IsZero() {
				existing.FinishedAt = report.FinishedAt
			}
			if strings.TrimSpace(report.Status) != "" {
				existing.Status = strings.TrimSpace(report.Status)
			}
			if strings.TrimSpace(report.ResultPath) != "" {
				existing.ResultPath = strings.TrimSpace(report.ResultPath)
			}
			if strings.TrimSpace(report.Error) != "" {
				existing.Error = strings.TrimSpace(report.Error)
			}
			if report.PreviewChars > 0 {
				existing.PreviewChars = report.PreviewChars
			}
			existing.ReportedAt = now
			current.ReportedAgentResults[id] = existing
		}

		current.Version = runUIStateVersion
		current.UpdatedAt = now
		if len(current.HiddenAgents) == 0 {
			current.HiddenAgents = nil
		}
		if len(current.ReportedAgentResults) == 0 {
			current.ReportedAgentResults = nil
		}
		if err := writeJSONAtomic(path, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	if err != nil {
		return RunUIState{}, err
	}
	return updated, nil
}
