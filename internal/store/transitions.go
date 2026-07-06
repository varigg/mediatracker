package store

import "slices"

// legality: forward moves may skip in_progress; in_progress→want_to is an
// undo; done→in_progress is a re-consume; abandoned can be revived;
// self- and terminal→terminal transitions are illegal.
var legalTransitions = map[State][]State{
	StateWantTo:     {StateInProgress, StateDone, StateAbandoned},
	StateInProgress: {StateWantTo, StateDone, StateAbandoned},
	StateDone:       {StateInProgress},
	StateAbandoned:  {StateWantTo, StateInProgress},
}

// CanTransition reports whether a lifecycle move from → to is legal.
func CanTransition(from, to State) bool {
	return slices.Contains(legalTransitions[from], to)
}

// LegalTransitions lists the states reachable from the given state, in a
// stable order for rendering.
func LegalTransitions(from State) []State {
	return legalTransitions[from]
}
