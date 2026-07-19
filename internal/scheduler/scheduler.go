package scheduler

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"gfw-watchdog/internal/tracker"
)

type DurationRange struct {
	Min time.Duration
	Max time.Duration
}

func ParseDurationRange(s string) (DurationRange, error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return DurationRange{}, fmt.Errorf("invalid range %q, want MIN-MAX e.g. 10h-20h", s)
	}
	min, err := time.ParseDuration(strings.TrimSpace(parts[0]))
	if err != nil {
		return DurationRange{}, fmt.Errorf("invalid minimum in range %q: %w", s, err)
	}
	max, err := time.ParseDuration(strings.TrimSpace(parts[1]))
	if err != nil {
		return DurationRange{}, fmt.Errorf("invalid maximum in range %q: %w", s, err)
	}
	if min < 0 || max < 0 {
		return DurationRange{}, fmt.Errorf("durations must not be negative")
	}
	if min > max {
		return DurationRange{}, fmt.Errorf("min (%s) > max (%s)", min, max)
	}
	return DurationRange{Min: min, Max: max}, nil
}

func (r DurationRange) Sample() time.Duration {
	if r.Max <= r.Min {
		return r.Min
	}
	return r.Min + time.Duration(rand.Int64N(int64(r.Max-r.Min)))
}

type IntervalConfig struct {
	Interval        DurationRange
	BlockedCooldown DurationRange
}

func NextInterval(state *tracker.TargetState, cfg IntervalConfig) time.Duration {
	current, consecutiveOK, _ := state.Snapshot()
	if !state.IsControl && current == tracker.StateBlocked && consecutiveOK == 0 {
		return cfg.BlockedCooldown.Sample()
	}
	return cfg.Interval.Sample()
}

func MonitorTarget(ctx context.Context, state *tracker.TargetState, cfg IntervalConfig, check func(context.Context) (bool, bool), onResult func(bool)) {
	runOnce := func() {
		success, valid := check(ctx)
		if valid {
			onResult(success)
		}
	}
	runOnce()
	timer := time.NewTimer(NextInterval(state, cfg))
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		runOnce()
		timer.Reset(NextInterval(state, cfg))
	}
}
