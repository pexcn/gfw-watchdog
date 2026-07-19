package scheduler

import (
	"testing"
	"time"

	"gfw-watchdog/internal/tracker"
)

func TestDurationRange(t *testing.T) {
	r, err := ParseDurationRange("20ms-40ms")
	if err != nil {
		t.Fatal(err)
	}
	for range 100 {
		got := r.Sample()
		if got < 20*time.Millisecond || got >= 40*time.Millisecond {
			t.Fatalf("sample out of range: %s", got)
		}
	}
	for _, invalid := range []string{"20s", "40s-20s", "-1s-2s", "bad-2s"} {
		if _, err := ParseDurationRange(invalid); err == nil {
			t.Errorf("expected %q to fail", invalid)
		}
	}
}

func TestNextInterval(t *testing.T) {
	cfg := IntervalConfig{
		Interval:        DurationRange{Min: time.Second, Max: time.Second},
		FailureRetry:    DurationRange{Min: time.Minute, Max: time.Minute},
		BlockedCooldown: DurationRange{Min: time.Hour, Max: time.Hour},
	}
	s := &tracker.TargetState{}
	s.Record(false, 2, 3)
	if got := NextInterval(s, cfg); got != time.Minute {
		t.Fatalf("got %s after unconfirmed failure", got)
	}
	s.Record(false, 2, 3)
	s.Record(false, 2, 3)
	if got := NextInterval(s, cfg); got != time.Hour {
		t.Fatalf("got %s", got)
	}
	s.Record(true, 2, 3)
	if got := NextInterval(s, cfg); got != time.Second {
		t.Fatalf("got %s after success", got)
	}
}

func TestControlUsesFailureRetryBeforeBlocked(t *testing.T) {
	cfg := IntervalConfig{
		Interval:        DurationRange{Min: time.Second, Max: time.Second},
		FailureRetry:    DurationRange{Min: time.Minute, Max: time.Minute},
		BlockedCooldown: DurationRange{Min: time.Hour, Max: time.Hour},
	}
	s := &tracker.TargetState{IsControl: true}
	s.Record(false, 1, 2)
	if got := NextInterval(s, cfg); got != time.Minute {
		t.Fatalf("got %s after control failure", got)
	}
	s.Record(false, 1, 2)
	if got := NextInterval(s, cfg); got != time.Second {
		t.Fatalf("blocked control must return to normal interval, got %s", got)
	}
}
