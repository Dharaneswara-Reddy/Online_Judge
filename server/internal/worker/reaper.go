package worker

import (
	"context"
	"log"
	"time"

	"github.com/toji339/online-judge/internal/submission"
)

// Defaults for the stale-submission sweep.
const (
	// DefaultStaleAfter is how long a submission may stay pending or
	// running before it is treated as abandoned.
	//
	// It has to clear the worst honest case comfortably: queue wait plus
	// the 60 second evaluation budget, times a redelivery. Fifteen
	// minutes is far beyond that, so nothing that is merely slow is ever
	// reclaimed, while a submission whose worker died is released long
	// before the user gives up on ever submitting again.
	DefaultStaleAfter = 15 * time.Minute

	// DefaultSweepInterval is how often the sweep runs. A single
	// conditional UpdateMany over an indexed filter is cheap enough to
	// run every minute, and it bounds how long a user is locked out to
	// roughly the staleness threshold rather than the threshold plus a
	// long wait for the next pass.
	DefaultSweepInterval = time.Minute

	// sweepTimeout bounds one sweep so a stalled database cannot wedge
	// the reaper goroutine until shutdown.
	sweepTimeout = 30 * time.Second
)

// Reaper reclaims submissions the judging pipeline never finished.
//
// Nothing else does. A worker can be killed, OOM-ed or cut off from the
// database between accepting a job and writing any status, and the queue
// can exhaust a job's retries; either way the submission stays
// non-terminal forever. Because admission control is a partial unique
// index over pending and running rows, that one stranded row silently
// bars the user from submitting anything ever again — a state only a
// hand-edit of the database could clear. War Rooms already have such a
// sweep (ExpireStale); submissions had none.
type Reaper struct {
	submissions *submission.Service
	staleAfter  time.Duration
	interval    time.Duration
}

// NewReaper builds a Reaper. Non-positive values fall back to the
// defaults above.
func NewReaper(submissions *submission.Service, staleAfter, interval time.Duration) *Reaper {
	if staleAfter <= 0 {
		staleAfter = DefaultStaleAfter
	}
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Reaper{submissions: submissions, staleAfter: staleAfter, interval: interval}
}

// Sweep reclaims everything currently stale and reports how many rows it
// closed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	return r.submissions.ReclaimStale(sweepCtx, r.staleAfter)
}

// Run sweeps on a ticker until the context is cancelled.
//
// A failed sweep is logged and the loop continues: the database being
// briefly unreachable is exactly the sort of incident that creates stale
// submissions, so this must survive it and clean up afterwards. It is
// safe to run in every worker process at once — the sweep is one
// conditional write, so overlapping passes cannot double-reclaim or
// clobber a verdict.
func (r *Reaper) Run(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()

	log.Printf("submission reaper started (every %s, reclaiming after %s)", r.interval, r.staleAfter)

	for {
		select {
		case <-ctx.Done():
			log.Println("submission reaper stopped")
			return
		case <-ticker.C:
			reclaimed, err := r.Sweep(ctx)
			if err != nil {
				if ctx.Err() != nil {
					continue
				}
				log.Printf("WARNING: submission reaper sweep failed: %v", err)
				continue
			}
			if reclaimed > 0 {
				log.Printf("submission reaper: reclaimed %d submission(s) stuck for over %s",
					reclaimed, r.staleAfter)
			}
		}
	}
}
