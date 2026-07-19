package tracker

import "sync"

type State int

const (
	StateUnknown State = iota
	StateOK
	StateBlocked
)

type TargetState struct {
	Key        string
	Current    State
	ConsecOK   int
	ConsecFail int
	IsControl  bool
	mu         sync.RWMutex
}

func (s *TargetState) Record(success bool, rise, fall int) (changed bool, from, to State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	from = s.Current
	if success {
		s.ConsecOK++
		s.ConsecFail = 0
		if s.ConsecOK >= rise {
			s.Current = StateOK
		}
	} else {
		s.ConsecFail++
		s.ConsecOK = 0
		if s.ConsecFail >= fall {
			s.Current = StateBlocked
		}
	}
	return from != StateUnknown && from != s.Current, from, s.Current
}

func (s *TargetState) Snapshot() (State, int, int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.Current, s.ConsecOK, s.ConsecFail
}

func ControlHealthy(states map[string]*TargetState, controlKeys []string) (healthy, hasControl bool) {
	if len(controlKeys) == 0 {
		return true, false
	}
	for _, key := range controlKeys {
		state, ok := states[key]
		if !ok {
			continue
		}
		current, _, _ := state.Snapshot()
		if current == StateOK {
			return true, true
		}
	}
	return false, true
}
