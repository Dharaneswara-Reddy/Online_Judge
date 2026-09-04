package assist

import (
	"strings"
	"testing"
	"time"
)

// base is an arbitrary fixed instant. Detect takes now as a parameter
// precisely so the suite can pin time and never sleep.
var base = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

// at builds one terminal attempt, submitted and judged minutesAgo
// before base.
func at(status string, failedCase int, minutesAgo float64) Attempt {
	submitted := base.Add(-time.Duration(minutesAgo * float64(time.Minute)))
	judged := submitted.Add(time.Second)
	return Attempt{
		Status:      status,
		FailedCase:  failedCase,
		TotalCases:  20,
		SubmittedAt: submitted,
		JudgedAt:    &judged,
	}
}

func TestDetect(t *testing.T) {
	tests := []struct {
		name       string
		attempts   []Attempt
		wantStuck  bool
		wantRung   int
		reasonHas  string
		wantStatus string
	}{
		{
			name:      "no attempts",
			attempts:  nil,
			wantStuck: false,
		},
		{
			name:       "a single failure is not stuck",
			attempts:   []Attempt{at("wrong_answer", 3, 2)},
			wantStuck:  false,
			wantStatus: "wrong_answer",
		},
		{
			name: "two unrelated failures are not stuck",
			attempts: []Attempt{
				at("wrong_answer", 3, 4),
				at("time_limit_exceeded", 9, 1),
			},
			wantStuck:  false,
			wantStatus: "time_limit_exceeded",
		},
		{
			name: "fixation on one hidden case",
			attempts: []Attempt{
				at("wrong_answer", 7, 3),
				at("wrong_answer", 7, 2),
				at("wrong_answer", 7, 1),
			},
			wantStuck:  true,
			wantRung:   3,
			reasonHas:  "same hidden test case",
			wantStatus: "wrong_answer",
		},
		{
			name: "burst of four submissions inside three minutes",
			attempts: []Attempt{
				at("wrong_answer", 1, 2.5),
				at("time_limit_exceeded", 2, 2.0),
				at("runtime_error", 3, 1.0),
				at("wrong_answer", 4, 0.2),
			},
			wantStuck:  true,
			wantRung:   2,
			reasonHas:  "three minutes",
			wantStatus: "wrong_answer",
		},
		{
			name: "oscillation between two failure modes",
			attempts: []Attempt{
				at("wrong_answer", 1, 20),
				at("time_limit_exceeded", 2, 15),
				at("wrong_answer", 3, 10),
				at("time_limit_exceeded", 4, 1),
			},
			wantStuck:  true,
			wantRung:   3,
			reasonHas:  "alternate",
			wantStatus: "time_limit_exceeded",
		},
		{
			name: "no progress since the verdict last changed",
			attempts: []Attempt{
				at("compile_error", 0, 30),
				at("wrong_answer", 1, 20),
				at("wrong_answer", 2, 12),
			},
			wantStuck:  true,
			wantRung:   4,
			reasonHas:  "verdict",
			wantStatus: "wrong_answer",
		},
		{
			name: "the highest rung wins when several rules fire",
			attempts: []Attempt{
				at("wrong_answer", 7, 40),
				at("wrong_answer", 7, 30),
				at("wrong_answer", 7, 20),
			},
			wantStuck:  true,
			wantRung:   4, // fixation says 3, no progress says 4
			reasonHas:  "verdict",
			wantStatus: "wrong_answer",
		},
		{
			name: "solving the problem clears every signal",
			attempts: []Attempt{
				at("wrong_answer", 7, 40),
				at("wrong_answer", 7, 30),
				at("wrong_answer", 7, 20),
				at("accepted", 0, 1),
			},
			wantStuck:  false,
			wantStatus: "accepted",
		},
		{
			name: "a submission still being judged is not a signal",
			attempts: []Attempt{
				at("wrong_answer", 7, 3),
				at("wrong_answer", 7, 2),
				{Status: "pending", SubmittedAt: base.Add(-time.Minute)},
			},
			wantStuck:  false,
			wantStatus: "wrong_answer",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.attempts, base)

			if got.Stuck != tc.wantStuck {
				t.Fatalf("Stuck = %v, want %v (reason %q)", got.Stuck, tc.wantStuck, got.Reason)
			}
			if got.MaxRung != tc.wantRung {
				t.Errorf("MaxRung = %d, want %d", got.MaxRung, tc.wantRung)
			}
			if tc.wantStatus != got.LastStatus {
				t.Errorf("LastStatus = %q, want %q", got.LastStatus, tc.wantStatus)
			}
			if tc.reasonHas != "" && !strings.Contains(got.Reason, tc.reasonHas) {
				t.Errorf("Reason = %q, want it to mention %q", got.Reason, tc.reasonHas)
			}
			if !tc.wantStuck && got.Reason != "" {
				t.Errorf("Reason = %q, want empty when not stuck", got.Reason)
			}
		})
	}
}

// TestDetectCountsOnlyTerminalAttempts pins the field the client renders
// as "you have tried N times".
func TestDetectCountsOnlyTerminalAttempts(t *testing.T) {
	got := Detect([]Attempt{
		at("wrong_answer", 1, 5),
		{Status: "running", SubmittedAt: base},
	}, base)

	if got.Attempts != 1 {
		t.Fatalf("Attempts = %d, want 1", got.Attempts)
	}
}

// TestDetectIgnoresAttemptsWithoutAJudgedAt guards the no-progress rule
// against a nil clock reading; a terminal row with no JudgedAt is a data
// bug, not a reason to panic.
func TestDetectIgnoresAttemptsWithoutAJudgedAt(t *testing.T) {
	a := at("wrong_answer", 1, 30)
	a.JudgedAt = nil
	b := at("wrong_answer", 2, 20)
	b.JudgedAt = nil
	c := at("wrong_answer", 3, 10)
	c.JudgedAt = nil

	got := Detect([]Attempt{a, b, c}, base)
	if got.Stuck {
		t.Fatalf("Stuck = true with no JudgedAt anywhere; reason %q", got.Reason)
	}
}
