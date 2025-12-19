package runner

import (
	"sync"

	"github.com/tiborv/kube-parcel/pkg/shared"
)

// StateMachine manages the orchestrator state
type StateMachine struct {
	mu           sync.RWMutex
	current      shared.State
	onTransition func(from, to shared.State)
	imagesCount  int
	chartsCount  int
}

// NewStateMachine creates a new state machine
func NewStateMachine() *StateMachine {
	return &StateMachine{
		current: shared.StateIdle,
	}
}

func (sm *StateMachine) Current() shared.State {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.current
}

func (sm *StateMachine) Transition(to shared.State) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	from := sm.current
	sm.current = to

	if sm.onTransition != nil {
		go sm.onTransition(from, to)
	}

	return nil
}

func (sm *StateMachine) OnTransition(fn func(from, to shared.State)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.onTransition = fn
}

func (sm *StateMachine) IncrementImages() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.imagesCount++
}

func (sm *StateMachine) IncrementCharts() {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.chartsCount++
}

func (sm *StateMachine) GetCounts() (images, charts int) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.imagesCount, sm.chartsCount
}
