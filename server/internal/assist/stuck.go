package assist

import (
	"fmt"
	"sort"
	"time"
)

// Stuck detection is deliberately the one part of this package that uses
// no model at all.
//
// The hard problem in an assistant that offers help is not writing the
// help — it is knowing when to offer, because an offer at the wrong
// moment is worse than no offer. A student reading a statement carefully
// for nine minutes is not stuck; a student who has submitted four times
// in ninety seconds is. Stuck has a shape, and every signal needed to
// see it is already in the submissions collection.
//
// So the trigger is a rule over data the judge already records. That
// keeps it free, instant, and — the part that matters — inspectable: a
// student can be told exactly which of their own attempts produced the
// offer, which is not something a model's judgement could support.
//
// now is a parameter rather than a call to time.Now so the suite can pin
// the clock and never sleep.

// Detection thresholds. Each is a judgement about what a pattern means,
// so each is named rather than inlined.
const (
	// minFailuresToConsider is the floor for every rule. Two failures is
	// ordinary debugging; interrupting there teaches people to dismiss
	// the assistant permanently on their second wrong answer.
	minFailuresToConsider = 2

	// fixationRepeats is how many failures on one hidden case reads as
	// fixation rather than iteration.
	fixationRepeats = 3

	// burstCount submissions inside burstWindow is guessing, not
	// reasoning: nobody reconsiders an approach in forty seconds.
	burstCount  = 4
	burstWindow = 3 * time.Minute

	// oscillationSpan is how many recent failures are inspected for the
	// two-mode pattern — fixing the time limit by breaking correctness,
	// then fixing correctness by breaking the time limit.
	oscillationSpan = 4

	// noProgressAfter is how long the same verdict has to stand before it
	// stops being persistence and starts being a wall.
	noProgressAfter = 6 * time.Minute
)

// statusAccepted is the one verdict that clears every signal. It is
// spelled here rather than imported from the submission package, because
// this package deliberately depends on nothing else in the codebase.
const statusAccepted = "accepted"

// statusCompileError is excluded from the fixation rule: its FailedCase
// is always zero, so three compile errors would otherwise look like
// three failures on hidden case zero.
const statusCompileError = "compile_error"

// State is what the client needs to decide whether to offer help, and
// what to say if it does.
//
// MaxRung is a ceiling, not an instruction. It says how far down the
// ladder the observed signals justify going — there is no point offering
// to characterise the failing case to someone who has not failed the
// same case twice — and the student still chooses each step.
type State struct {
	Stuck      bool   `json:"stuck"`
	Reason     string `json:"reason"`
	Attempts   int    `json:"attempts"`
	MaxRung    int    `json:"maxRung"`
	LastStatus string `json:"lastStatus"`
}

// finding is one rule's answer: how far down the ladder it justifies
// going, and the sentence shown to the student.
type finding struct {
	rung   int
	reason string
}

// Detect applies the rules to one student's attempts at one problem.
//
// attempts may arrive in any order and may include submissions that are
// still being judged; both are normalised here rather than at every call
// site. The returned State is safe to serialise straight to the client:
// it carries no test data, no code, and no verdict the student cannot
// already see in their own history.
func Detect(attempts []Attempt, now time.Time) State {
	terminal := terminalAttempts(attempts)

	state := State{Attempts: len(terminal)}
	if len(terminal) == 0 {
		return state
	}
	state.LastStatus = terminal[len(terminal)-1].Status

	// Solving the problem ends the story. Anything after an accepted
	// verdict is experimentation, not struggle, so failures are counted
	// only from the last solve onwards.
	failures := failuresSinceLastSolve(terminal)
	if len(failures) < minFailuresToConsider {
		return state
	}

	// Evaluated highest-ceiling first, so the loop below can stop
	// caring about order and simply keep the strongest signal.
	rules := []func([]Attempt, time.Time) (finding, bool){
		noProgressRule,
		fixationRule,
		oscillationRule,
		burstRule,
	}

	best := finding{}
	for _, rule := range rules {
		got, ok := rule(failures, now)
		if ok && got.rung > best.rung {
			best = got
		}
	}

	if best.rung == 0 {
		return state
	}

	state.Stuck = true
	state.MaxRung = best.rung
	state.Reason = best.reason
	return state
}

// terminalAttempts drops submissions the judge has not finished with and
// returns the rest oldest-first.
//
// A pending submission is not evidence of anything yet, and counting it
// would let a student trigger the offer by submitting rather than by
// struggling. The input slice is copied before sorting: callers hand us
// their own data and should not find it rearranged.
func terminalAttempts(attempts []Attempt) []Attempt {
	out := make([]Attempt, 0, len(attempts))
	for _, a := range attempts {
		switch a.Status {
		case "", "pending", "running":
			continue
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].SubmittedAt.Before(out[j].SubmittedAt)
	})
	return out
}

// failuresSinceLastSolve returns the non-accepted attempts that came
// after the most recent accepted one, or all of them if there was none.
func failuresSinceLastSolve(terminal []Attempt) []Attempt {
	start := 0
	for i, a := range terminal {
		if a.Status == statusAccepted {
			start = i + 1
		}
	}
	return terminal[start:]
}

// fixationRule fires when the same hidden case keeps winning.
//
// This is the signal that most deserves rung 3: the student is not short
// of ideas, they are short of one specific piece of information about
// the input they have never seen.
func fixationRule(failures []Attempt, _ time.Time) (finding, bool) {
	counts := map[int]int{}
	for _, a := range failures {
		if a.Status == statusCompileError {
			continue
		}
		counts[a.FailedCase]++
	}

	for _, n := range counts {
		if n >= fixationRepeats {
			return finding{
				rung:   3,
				reason: fmt.Sprintf("%d of your attempts failed on the same hidden test case.", n),
			}, true
		}
	}
	return finding{}, false
}

// burstRule fires on rapid resubmission, which is guessing.
//
// It earns only rung 2 on purpose. Someone changing a line and
// resubmitting does not need the failing case described to them; they
// need to be slowed down and pointed at the shape of the problem.
func burstRule(failures []Attempt, _ time.Time) (finding, bool) {
	for i := 0; i+burstCount-1 < len(failures); i++ {
		span := failures[i+burstCount-1].SubmittedAt.Sub(failures[i].SubmittedAt)
		if span <= burstWindow {
			return finding{
				rung:   2,
				reason: fmt.Sprintf("You have made %d submissions in under three minutes.", burstCount),
			}, true
		}
	}
	return finding{}, false
}

// oscillationRule fires when the last few verdicts flip between exactly
// two failure modes — the classic sign of fixing one limit by breaking
// another, which no amount of further iteration resolves.
func oscillationRule(failures []Attempt, _ time.Time) (finding, bool) {
	if len(failures) < oscillationSpan {
		return finding{}, false
	}
	recent := failures[len(failures)-oscillationSpan:]

	distinct := map[string]bool{}
	for i, a := range recent {
		distinct[a.Status] = true
		if i > 0 && a.Status == recent[i-1].Status {
			return finding{}, false
		}
	}
	if len(distinct) != 2 {
		return finding{}, false
	}

	return finding{
		rung: 3,
		reason: fmt.Sprintf("Your recent verdicts alternate between %s and %s.",
			recent[len(recent)-2].Status, recent[len(recent)-1].Status),
	}, true
}

// noProgressRule fires when the same verdict has stood for a while.
//
// It carries the highest ceiling because it is the signal that most
// often precedes someone closing the tab: not a burst of activity but
// the absence of one, with nothing having changed for long enough that
// the next attempt is unlikely to differ either.
//
// A terminal attempt with no JudgedAt is a data bug rather than a
// signal, and the rule declines to guess a time for it.
func noProgressRule(failures []Attempt, now time.Time) (finding, bool) {
	if len(failures) < fixationRepeats {
		return finding{}, false
	}

	// Walk back through the trailing run of identical verdicts to find
	// when this verdict was first reached.
	last := failures[len(failures)-1]
	anchor := last
	for i := len(failures) - 2; i >= 0; i-- {
		if failures[i].Status != last.Status {
			break
		}
		anchor = failures[i]
	}
	if anchor.JudgedAt == nil {
		return finding{}, false
	}

	stale := now.Sub(*anchor.JudgedAt)
	if stale < noProgressAfter {
		return finding{}, false
	}

	return finding{
		rung:   4,
		reason: fmt.Sprintf("Your verdict has not changed in %d minutes.", int(stale.Minutes())),
	}, true
}
