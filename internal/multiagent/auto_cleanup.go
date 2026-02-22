package multiagent

import (
	"context"
	"fmt"
	"time"
)

type AutoCleanupRunner struct {
	Done <-chan struct{}
}

func RunAutoCleanupOnce(ctx context.Context, coord *Coordinator, cfg ResolvedAutoCleanupConfig) (RunPruneReport, error) {
	return PruneRuns(ctx, coord, RunPruneOptions{
		Mode:          RunPruneMode(cfg.Mode),
		ArchiveDir:    cfg.ArchiveDir,
		KeepLast:      cfg.KeepLast,
		ArchiveAfter:  cfg.ArchiveAfter,
		IncludeFailed: cfg.IncludeFailed,
		DryRun:        cfg.DryRun,
	})
}

func StartAutoCleanup(ctx context.Context, coord *Coordinator, cfg ResolvedAutoCleanupConfig, logf func(string)) (*AutoCleanupRunner, error) {
	if coord == nil {
		return nil, fmt.Errorf("coordinator is nil")
	}
	if !cfg.Enabled {
		return &AutoCleanupRunner{Done: closedChan()}, nil
	}
	if cfg.Interval <= 0 {
		return nil, fmt.Errorf("invalid cleanup interval: %s", cfg.Interval.String())
	}

	done := make(chan struct{})
	go func() {
		defer close(done)

		runOnce := func(trigger string) {
			report, err := RunAutoCleanupOnce(ctx, coord, cfg)
			if err != nil {
				if logf != nil {
					logf(fmt.Sprintf("[multi-agent cleanup] %s error: %v", trigger, err))
				}
				return
			}
			if report.Applied == 0 && len(report.Errors) == 0 {
				return
			}
			if logf != nil {
				logf(fmt.Sprintf(
					"[multi-agent cleanup] %s mode=%s dry_run=%v applied=%d candidates=%d errors=%d",
					trigger,
					report.Mode,
					report.DryRun,
					report.Applied,
					report.PruneCandidates,
					len(report.Errors),
				))
			}
		}

		runOnce("startup")
		ticker := time.NewTicker(cfg.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runOnce("tick")
			}
		}
	}()

	return &AutoCleanupRunner{Done: done}, nil
}

func closedChan() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
