package multiagent

import (
	"errors"
	"os"
	"testing"
)

func TestCoordinatorSetRunTitleAndDeleteRun(t *testing.T) {
	t.Parallel()

	coord := NewCoordinator(t.TempDir())
	run, err := coord.CreateRun("demo-run", nil)
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	updated, err := coord.SetRunTitle(run.ID, "demo title")
	if err != nil {
		t.Fatalf("SetRunTitle failed: %v", err)
	}
	if got := RunTitle(updated); got != "demo title" {
		t.Fatalf("expected title %q, got %q", "demo title", got)
	}

	loaded, err := coord.ReadRun(run.ID)
	if err != nil {
		t.Fatalf("ReadRun failed: %v", err)
	}
	if got := RunTitle(loaded); got != "demo title" {
		t.Fatalf("expected persisted title %q, got %q", "demo title", got)
	}

	if err := coord.DeleteRun(run.ID); err != nil {
		t.Fatalf("DeleteRun failed: %v", err)
	}
	if _, err := coord.ReadRun(run.ID); err == nil {
		t.Fatalf("expected ReadRun error after deletion")
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected os.ErrNotExist after deletion, got %v", err)
	}
}

