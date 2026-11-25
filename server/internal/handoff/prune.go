package handoff

import (
	"context"
	"log/slog"
	"time"

	"quakerelay/server/internal/store"
)

// Retention defaults from the VPS integration design: 15 minutes or 10,000 rows,
// whichever removes more. A reader behind the removed range sees gap=true.
const (
	RetainAge   = 15 * time.Minute
	RetainRows  = 10000
	prunePeriod = 30 * time.Second
)

// RunPruner removes expired hand-off rows until ctx ends.
func RunPruner(ctx context.Context, st *store.Store, logger *slog.Logger) {
	ticker := time.NewTicker(prunePeriod)
	defer ticker.Stop()
	for {
		if removed, err := st.PruneHandoff(ctx, time.Now(), RetainAge, RetainRows); err != nil {
			if ctx.Err() != nil {
				return
			}
			logger.Warn("EEW hand-off pruning failed", "error", err.Error())
		} else if removed > 0 {
			logger.Info("EEW hand-off pruned", "rows", removed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
