package tracker

import "testing"

func TestTransitionsAndBaseline(t *testing.T) {
	s := &TargetState{}
	changed, _, _ := s.Record(false, 2, 3)
	if changed {
		t.Fatal("baseline must not emit a change")
	}
	s.Record(false, 2, 3)
	changed, _, state := s.Record(false, 2, 3)
	if changed || state != StateBlocked {
		t.Fatalf("initial blocked baseline: changed=%v state=%v", changed, state)
	}
	s.Record(true, 2, 3)
	changed, _, state = s.Record(true, 2, 3)
	if !changed || state != StateOK {
		t.Fatalf("recovery: changed=%v state=%v", changed, state)
	}
	s.Record(false, 2, 3)
	s.Record(false, 2, 3)
	changed, _, state = s.Record(false, 2, 3)
	if !changed || state != StateBlocked {
		t.Fatalf("blocked: changed=%v state=%v", changed, state)
	}
}

func TestControlHealthyRequiresAllControlsDown(t *testing.T) {
	okState := &TargetState{Current: StateOK}
	downState := &TargetState{Current: StateBlocked}
	states := map[string]*TargetState{"ok": okState, "down": downState}
	if healthy, hasControl := ControlHealthy(states, []string{"ok", "down"}); !healthy || !hasControl {
		t.Fatalf("one healthy control should keep controls healthy: healthy=%v has=%v", healthy, hasControl)
	}
	if healthy, hasControl := ControlHealthy(states, []string{"down"}); healthy || !hasControl {
		t.Fatalf("all controls down should be unhealthy: healthy=%v has=%v", healthy, hasControl)
	}
}
