package monitor

// State is the pair the state machine evolves: current status plus how many
// consecutive failures have been observed since the last success.
type State struct {
	Status              Status
	ConsecutiveFailures int
}

// Apply advances the monitor state machine by one check result
// (spec section 3.2):
//
//   - UP/PENDING -> DOWN only after `failureThreshold` consecutive failures
//   - DOWN -> UP on the first success
//   - PAUSED ignores checks entirely (the scheduler shouldn't even be
//     checking a paused monitor; this is defense in depth)
//
// It returns the next state and whether a status transition occurred —
// transitions are what create incidents and fire notifications in M2.
func Apply(current Status, consecutiveFailures int, checkOK bool, failureThreshold int) (State, bool) {
	if current == StatusPaused {
		return State{Status: StatusPaused, ConsecutiveFailures: consecutiveFailures}, false
	}
	if failureThreshold < 1 {
		failureThreshold = 1
	}

	if checkOK {
		next := State{Status: StatusUp, ConsecutiveFailures: 0}
		return next, current != StatusUp
	}

	fails := consecutiveFailures + 1
	if fails >= failureThreshold {
		// Cap the stored counter at threshold so it can't grow unboundedly
		// while a monitor stays down.
		return State{Status: StatusDown, ConsecutiveFailures: failureThreshold}, current != StatusDown
	}
	return State{Status: current, ConsecutiveFailures: fails}, false
}
