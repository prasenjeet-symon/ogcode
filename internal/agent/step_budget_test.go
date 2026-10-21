package agent

import (
	"math"
	"testing"
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
