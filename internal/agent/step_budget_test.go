package agent

import (
	"math"
	"testing"

	"github.com/prasenjeet-symon/ogcode/internal/session"
)

// The step limits a run gets from MaxSteps/MaxAutoResumes. Zero keeps its
// historical "default budget" meaning for a zero-value LoopRunner, a negative
// value asks for no ceiling, and auto-resume multiplies the ceiling — except for
// an uncapped run, where extending is pointless and the multiply would overflow.
func TestStepBudgets(t *testing.T) {
	cases := []struct {
		name     string
		maxSteps int
		resumes  int
		wantMax  int
		wantHard int
	}{
		{name: "zero uses the default budget", maxSteps: 0, wantMax: 1000, wantHard: 1000},
		{name: "explicit cap", maxSteps: 100, wantMax: 100, wantHard: 100},
		{name: "auto-resume extends", maxSteps: 10000, resumes: 2, wantMax: 10000, wantHard: 30000},
		{name: "uncapped", maxSteps: MaxStepsUncapped, wantMax: math.MaxInt, wantHard: math.MaxInt},
		{name: "uncapped ignores auto-resume", maxSteps: MaxStepsUncapped, resumes: 2, wantMax: math.MaxInt, wantHard: math.MaxInt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr := &LoopRunner{MaxSteps: tc.maxSteps, MaxAutoResumes: tc.resumes}
			gotMax, gotHard := stepBudgets(lr)
			if gotMax != tc.wantMax || gotHard != tc.wantHard {
				t.Fatalf("stepBudgets = (%d, %d), want (%d, %d)", gotMax, gotHard, tc.wantMax, tc.wantHard)
			}
		})
	}
}

// The server's per-session override, applied over the runner's own limits. Zero
// means the hook has no opinion; a negative answer asks for no ceiling, which is
// how a teammate runs.
func TestOverrideStepBudgets(t *testing.T) {
	cases := []struct {
		name     string
		hook     func(session.SessionID) (int, int)
		wantMax  int
		wantHard int
	}{
		{name: "no hook keeps the runner's limits", wantMax: 10000, wantHard: 30000},
		{name: "zero leaves the answer in place", hook: func(session.SessionID) (int, int) { return 0, 0 }, wantMax: 10000, wantHard: 30000},
		{name: "uncapped", hook: func(session.SessionID) (int, int) { return MaxStepsUncapped, 0 }, wantMax: math.MaxInt, wantHard: math.MaxInt},
		{name: "uncapped ignores auto-resume", hook: func(session.SessionID) (int, int) { return MaxStepsUncapped, 2 }, wantMax: math.MaxInt, wantHard: math.MaxInt},
		{name: "capped override", hook: func(session.SessionID) (int, int) { return 300, 0 }, wantMax: 300, wantHard: 300},
		{name: "capped override with auto-resume", hook: func(session.SessionID) (int, int) { return 100, 2 }, wantMax: 100, wantHard: 300},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lr := &LoopRunner{MaxSteps: 10000, MaxAutoResumes: 2, StepBudgetFor: tc.hook}
			baseMax, baseHard := stepBudgets(lr)
			gotMax, gotHard := overrideStepBudgets(lr, "s1", baseMax, baseHard)
			if gotMax != tc.wantMax || gotHard != tc.wantHard {
				t.Fatalf("overrideStepBudgets = (%d, %d), want (%d, %d)", gotMax, gotHard, tc.wantMax, tc.wantHard)
			}
		})
	}
}
