package ha

import "sync"

// State is a role/lifecycle state in the HA state machine.
//
// Per this unit's task scope this is intentionally narrower than
// docs/design/ha-cluster.md's seven-state sketch: there is no distinct
// WAITING_ELECTION or FAILOVER state. Elections resolve directly into
// SECONDARY or PRIMARY (the design's WAITING_ELECTION was only ever a
// pre-first-election placeholder, folded here into INITIALIZING), and a
// state transition's side effects (VIP claim/release, immediate heartbeat)
// stand in for the design's transient FAILOVER state.
type State string

const (
	// StateInitializing is the state a Manager starts in before its first
	// election result (or, in standalone mode, before self-promotion).
	StateInitializing State = "INITIALIZING"

	// StateSecondary means this node answers DHCP (docs/design/ha-cluster.md:
	// secondaries are "hot," not passive) but does not hold the VIP and does
	// not write heartbeats.
	StateSecondary State = "SECONDARY"

	// StatePrimary means this node holds the cluster VIP (if HA is enabled)
	// and writes its own heartbeat every heartbeat_interval_ms.
	StatePrimary State = "PRIMARY"

	// StateError is the fencing state: the database has been unreachable
	// beyond the fencing grace period (heartbeat_ttl_ms — capped there, not
	// longer, so this node always fences itself no later than a peer would
	// consider its heartbeat stale enough to bootstrap-elect a replacement).
	// The node drops the VIP and ShouldServe() becomes false rather than
	// risk serving with stale/unverifiable cluster state.
	StateError State = "ERROR"

	// StateShutdown is terminal: reached once and never left. Reaching it
	// drops the VIP as a side effect (see cluster.go's transitionTo).
	StateShutdown State = "SHUTDOWN"
)

// allowedTransitions enumerates every legal edge. Notably ERROR has no edge
// to PRIMARY: a node recovering from a fencing episode must always rejoin as
// SECONDARY first and let the next normal election cycle decide whether it
// should be promoted (docs/design/ha-cluster.md's split-brain-avoidance
// rationale — a node that just regained DB access has no way to know
// whether a peer was promoted while it was fenced off).
var allowedTransitions = map[State]map[State]bool{
	StateInitializing: {StateSecondary: true, StatePrimary: true, StateError: true, StateShutdown: true},
	StateSecondary:    {StatePrimary: true, StateError: true, StateShutdown: true},
	StatePrimary:      {StateSecondary: true, StateError: true, StateShutdown: true},
	StateError:        {StateSecondary: true, StateShutdown: true},
	StateShutdown:     {},
}

// stateMachine holds the current state and enforces which transitions are
// legal. It has no side effects of its own: Manager (cluster.go's
// transitionTo) performs the VIP/heartbeat/logging/metrics actions around
// each transition that actually takes effect. Keeping the FSM's legality
// rules separate from those actions is what makes both independently
// testable.
type stateMachine struct {
	mu      sync.RWMutex
	current State
}

func newStateMachine() *stateMachine {
	return &stateMachine{current: StateInitializing}
}

// Current returns the current state.
func (s *stateMachine) Current() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Transition moves the machine to `to` if that edge is legal, returning
// true if the state actually changed. A no-op request (to == current) or an
// illegal edge returns false and leaves the state untouched; callers should
// treat "no-op" as normal (elections that don't change the outcome run
// every election_interval_ms) and treat "illegal edge" as a logic error
// worth logging (cluster.go's transitionTo logs every attempted
// transition's before/after so a rejected one is visible).
func (s *stateMachine) Transition(to State) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current == to {
		return false
	}
	if !allowedTransitions[s.current][to] {
		return false
	}
	s.current = to
	return true
}
