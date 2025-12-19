package runner

import (
	"sync"
	"testing"
	"time"

	"github.com/tiborv/kube-parcel/pkg/shared"
)

func TestNewStateMachine(t *testing.T) {
	sm := NewStateMachine()
	if sm == nil {
		t.Fatal("NewStateMachine returned nil")
	}
	if sm.Current() != shared.StateIdle {
		t.Errorf("expected initial state IDLE, got %v", sm.Current())
	}
}

func TestStateMachine_Transition(t *testing.T) {
	sm := NewStateMachine()

	transitions := []struct {
		to       shared.State
		expected shared.State
	}{
		{shared.StateTransferring, shared.StateTransferring},
		{shared.StateStarting, shared.StateStarting},
		{shared.StateReady, shared.StateReady},
		{shared.StateIdle, shared.StateIdle},
	}

	for _, tc := range transitions {
		if err := sm.Transition(tc.to); err != nil {
			t.Errorf("Transition to %v failed: %v", tc.to, err)
		}
		if sm.Current() != tc.expected {
			t.Errorf("expected state %v, got %v", tc.expected, sm.Current())
		}
	}
}

func TestStateMachine_OnTransition(t *testing.T) {
	sm := NewStateMachine()

	var called bool
	var fromState, toState shared.State

	sm.OnTransition(func(from, to shared.State) {
		called = true
		fromState = from
		toState = to
	})

	sm.Transition(shared.StateTransferring)

	// Wait a bit for the goroutine to execute
	time.Sleep(50 * time.Millisecond)

	if !called {
		t.Error("OnTransition callback was not called")
	}
	if fromState != shared.StateIdle {
		t.Errorf("expected from state IDLE, got %v", fromState)
	}
	if toState != shared.StateTransferring {
		t.Errorf("expected to state TRANSFERRING, got %v", toState)
	}
}

func TestStateMachine_IncrementImages(t *testing.T) {
	sm := NewStateMachine()

	for i := 0; i < 5; i++ {
		sm.IncrementImages()
	}

	images, _ := sm.GetCounts()
	if images != 5 {
		t.Errorf("expected 5 images, got %d", images)
	}
}

func TestStateMachine_IncrementCharts(t *testing.T) {
	sm := NewStateMachine()

	for i := 0; i < 3; i++ {
		sm.IncrementCharts()
	}

	_, charts := sm.GetCounts()
	if charts != 3 {
		t.Errorf("expected 3 charts, got %d", charts)
	}
}

func TestStateMachine_GetCounts(t *testing.T) {
	sm := NewStateMachine()

	sm.IncrementImages()
	sm.IncrementImages()
	sm.IncrementCharts()

	images, charts := sm.GetCounts()
	if images != 2 {
		t.Errorf("expected 2 images, got %d", images)
	}
	if charts != 1 {
		t.Errorf("expected 1 chart, got %d", charts)
	}
}

func TestStateMachine_ConcurrentAccess(t *testing.T) {
	sm := NewStateMachine()
	var wg sync.WaitGroup

	// Concurrent increments
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			sm.IncrementImages()
		}()
		go func() {
			defer wg.Done()
			sm.IncrementCharts()
		}()
	}

	wg.Wait()

	images, charts := sm.GetCounts()
	if images != 100 {
		t.Errorf("expected 100 images, got %d", images)
	}
	if charts != 100 {
		t.Errorf("expected 100 charts, got %d", charts)
	}
}

func TestStateMachine_ConcurrentTransitions(t *testing.T) {
	sm := NewStateMachine()
	var wg sync.WaitGroup

	states := []shared.State{
		shared.StateTransferring,
		shared.StateStarting,
		shared.StateReady,
		shared.StateIdle,
	}

	// Simulate concurrent transitions
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sm.Transition(states[idx%len(states)])
		}(i)
	}

	wg.Wait()

	// Just verify no panic occurred and we have a valid state
	current := sm.Current()
	validState := false
	for _, s := range states {
		if current == s {
			validState = true
			break
		}
	}
	if !validState {
		t.Errorf("invalid state after concurrent transitions: %v", current)
	}
}
