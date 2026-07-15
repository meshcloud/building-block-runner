package report

import (
	"context"
	"log/slog"
	"reflect"
	"time"
)

// Observer runs the status-report ticker for one run. It is TF-ONLY: tf is the only runner
// using the Progress+Observer 10s ticker; the four ports call Reporter.Report on
// state changes only, own their own step dedup, and never construct an Observer.
//
// Every Interval, Observer computes the diff of steps changed since the last send (the running
// step whose log grew, plus any steps that changed status — see diffSteps) and Reports ONLY
// those changed steps, honoring the "Report transmits only the changed/new steps" contract.
// The abort flag returned by Report cancels the run context via Cancel. On completion (Done
// fires) it sends one final update — mapping SUCCEEDED to IN_PROGRESS for async runs, since
// handing over to an external pipeline is not itself the run's true completion — unless
// the context was already cancelled by an abort, in which case nobody wants a final update for
// a run that was just told to stop (the "cancelled ctx => no final update" pin).
type Observer struct {
	// Interval between status ticks. Pinned at 10s for tf.
	Interval time.Duration
	// Reporter is the status backchannel this run reports through.
	Reporter Reporter
	// Async marks an async (pipeline-handover) run: its final SUCCEEDED is downgraded to
	// IN_PROGRESS because execution has only been handed off, not completed.
	Async bool
	// Log receives ticker lifecycle diagnostics (send failures). Falls back to slog.Default()
	// when nil.
	Log *slog.Logger
}

// Run blocks until done fires or ctx is cancelled, ticking Reporter.Report every Interval and
// sending one final report when done fires. cancel is invoked when a Report response carries
// the abort flag — Run keeps running afterwards (it still needs to observe done to send, or
// skip, the final update), it merely stops driving further work via the cancelled context.
//
// This takes one more parameter than an illustrative 3-arg sketch
// (`Run(ctx, cancel, p)`): a done signal distinct from ctx cancellation is required to tell
// "run finished, send final status" apart from "run was aborted, send nothing" — both end the
// loop, but only one sends a final report. ctx is not reused for both, matching the tf
// predecessor's workCtx (execution deadline/cancellation) plus doneSignallingChan (completion)
// split.
func (o Observer) Run(ctx context.Context, cancel context.CancelFunc, done <-chan struct{}, p *Progress) {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}

	ticker := time.NewTicker(o.Interval)
	defer ticker.Stop()

	var lastSent []StepStatus

	for {
		select {
		case <-done:
			// A cancelled context here can only mean an abort response already told the
			// coordinator to stop; sending a further "final" update is neither wanted nor safe
			// (no-final-after-cancel).
			if ctx.Err() != nil {
				return
			}

			final := p.Snapshot()
			if o.Async && final.Status == SUCCEEDED {
				final.Status = IN_PROGRESS
			}
			final.Steps = diffSteps(lastSent, final.Steps)

			if _, err := o.Reporter.Report(final); err != nil {
				log.Warn("failed to send final status update", "runId", final.RunId, "error", err)
			}
			return

		case <-ticker.C:
			snap := p.Snapshot()
			// A terminal status observed here means the work goroutine is about to signal
			// done; skip this tick so the final update (above) is the only terminal send.
			if snap.Status.IsTerminal() {
				continue
			}

			changed := diffSteps(lastSent, snap.Steps)
			toSend := snap
			toSend.Steps = changed

			abort, err := o.Reporter.Report(toSend)
			if err != nil {
				log.Warn("failed to send status update", "runId", toSend.RunId, "error", err)
				// Retry the same diff next tick rather than losing track of unsent changes.
				continue
			}
			lastSent = snap.Steps

			if abort {
				log.Info("received abort flag, cancelling run context", "runId", toSend.RunId)
				cancel()
			}
		}
	}
}

// diffSteps returns the elements of curr that are new or differ from the same-named element of
// prev, in curr's order. Matching by Name (the step id) rather than index tolerates steps being
// appended, and reflect.DeepEqual (which follows pointers) makes "differs" mean "differs in any
// observable field" without hand-rolling per-field comparisons that would drift from StepStatus
// as it grows fields.
func diffSteps(prev, curr []StepStatus) []StepStatus {
	if len(curr) == 0 {
		return nil
	}

	prevByName := make(map[string]StepStatus, len(prev))
	for _, s := range prev {
		prevByName[s.Name] = s
	}

	var changed []StepStatus
	for _, s := range curr {
		if old, ok := prevByName[s.Name]; !ok || !reflect.DeepEqual(old, s) {
			changed = append(changed, s)
		}
	}
	return changed
}
