package ha

import "testing"

func TestStateMachine_InitialState(t *testing.T) {
	sm := newStateMachine()
	if got := sm.Current(); got != StateInitializing {
		t.Fatalf("initial state = %s, want %s", got, StateInitializing)
	}
}

func TestStateMachine_LegalTransitions(t *testing.T) {
	cases := []struct {
		from, to State
	}{
		{StateInitializing, StateSecondary},
		{StateInitializing, StatePrimary},
		{StateInitializing, StateError},
		{StateSecondary, StatePrimary},
		{StatePrimary, StateSecondary},
		{StatePrimary, StateError},
		{StateError, StateSecondary},
		{StateSecondary, StateShutdown},
		{StatePrimary, StateShutdown},
		{StateError, StateShutdown},
	}
	for _, tc := range cases {
		sm := &stateMachine{current: tc.from}
		if ok := sm.Transition(tc.to); !ok {
			t.Errorf("%s -> %s: want legal, got rejected", tc.from, tc.to)
		}
		if got := sm.Current(); got != tc.to {
			t.Errorf("%s -> %s: current = %s, want %s", tc.from, tc.to, got, tc.to)
		}
	}
}

func TestStateMachine_ErrorNeverGoesDirectlyToPrimary(t *testing.T) {
	sm := &stateMachine{current: StateError}
	if ok := sm.Transition(StatePrimary); ok {
		t.Fatal("ERROR -> PRIMARY should be illegal: a recovering node must rejoin as SECONDARY first")
	}
	if got := sm.Current(); got != StateError {
		t.Fatalf("state changed despite illegal transition: got %s", got)
	}
}

func TestStateMachine_ShutdownIsTerminal(t *testing.T) {
	sm := &stateMachine{current: StateShutdown}
	for _, to := range []State{StateInitializing, StateSecondary, StatePrimary, StateError} {
		if ok := sm.Transition(to); ok {
			t.Errorf("SHUTDOWN -> %s should be illegal (terminal state)", to)
		}
	}
}

func TestStateMachine_SameStateIsNoOp(t *testing.T) {
	sm := &stateMachine{current: StatePrimary}
	if ok := sm.Transition(StatePrimary); ok {
		t.Fatal("transitioning to the current state should report no-op (false)")
	}
	if got := sm.Current(); got != StatePrimary {
		t.Fatalf("state = %s, want unchanged PRIMARY", got)
	}
}
