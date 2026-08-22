package worker

import (
	"context"
	"log"
	"time"

	"github.com/toji339/online-judge/internal/submission"
)

// Defaults for the stale-submission sweep.
const (
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
	interval    time.Duration
}

// NewReaper builds a Reaper. A non-positive interval falls back to the
// default above.
//
// How stale is stale is not a parameter here: submission.ExpireStale
// owns that, because pending and running rows deserve different cutoffs
// — a queued submission may legitimately wait far longer than one a
// worker has already picked up — and a single duration passed in from
// the caller could not express the difference.
func NewReaper(submissions *submission.Service, interval time.Duration) *Reaper {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	return &Reaper{submissions: submissions, interval: interval}
}

// Sweep reclaims everything currently stale and reports how many rows it
// closed.
func (r *Reaper) Sweep(ctx context.Context) (int, error) {
	sweepCtx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()

	return r.submissions.ExpireStale(sweepCtx)
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

	log.Printf("submission reaper started (every %s; reclaims pending after %s, running after %s)",
		r.interval, submission.PendingTTL, submission.RunningTTL)

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
				log.Printf("submission reaper: reclaimed %d abandoned submission(s)", reclaimed)
			}
		}
	}
}
