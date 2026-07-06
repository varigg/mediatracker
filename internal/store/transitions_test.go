package store

import (
	"reflect"
	"testing"
)

// Exhaustive 4x4 matrix (plus unknown-state guards). Kept as an explicit
// table so a legality change is a visible diff here, not an accident.
func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to State
		want     bool
	}{
		{StateWantTo, StateWantTo, false},
		{StateWantTo, StateInProgress, true},
		{StateWantTo, StateDone, true},
		{StateWantTo, StateAbandoned, true},

		{StateInProgress, StateWantTo, true},
		{StateInProgress, StateInProgress, false},
		{StateInProgress, StateDone, true},
		{StateInProgress, StateAbandoned, true},

		{StateDone, StateWantTo, false},
		{StateDone, StateInProgress, true},
		{StateDone, StateDone, false},
		{StateDone, StateAbandoned, false},

		{StateAbandoned, StateWantTo, true},
		{StateAbandoned, StateInProgress, true},
		{StateAbandoned, StateDone, false},
		{StateAbandoned, StateAbandoned, false},

		{State("bogus"), StateDone, false},
		{StateWantTo, State("bogus"), false},
	}
	for _, c := range cases {
		if got := CanTransition(c.from, c.to); got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestLegalTransitions(t *testing.T) {
	got := LegalTransitions(StateDone)
	want := []State{StateInProgress}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LegalTransitions(done) = %v, want %v", got, want)
	}
}
