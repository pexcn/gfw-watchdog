package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	watchdog "gfw-watchdog"
	"gfw-watchdog/internal/prober"
	"gfw-watchdog/internal/scheduler"
	"gfw-watchdog/internal/target"
	"gfw-watchdog/internal/tracker"
	"gfw-watchdog/webhook"
)

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func main() {
	log.SetOutput(os.Stdout)
	if wantsHelp(os.Args[1:]) {
		watchdog.PrintUsage(os.Stdout)
		return
	}
	cfg, err := watchdog.ParseConfig(watchdog.TranslateShortArgs(os.Args[1:]), os.Getenv("WEBHOOKS"))
	if err != nil {
		log.Print(err)
		os.Exit(2)
	}
	if err := run(cfg); err != nil {
		log.Fatal(err)
	}
}

func run(cfg watchdog.Config) error {
	targets := target.Expand(cfg.Targets, false)
	controls := target.Expand(cfg.Controls, true)
	allTargets := append(append([]target.Target{}, targets...), controls...)
	needV4ICMP, needV6ICMP := false, false
	for _, t := range allTargets {
		if t.Kind != target.ProbeICMP {
			continue
		}
		if t.IP.To4() != nil {
			needV4ICMP = true
		} else {
			needV6ICMP = true
		}
	}
	var icmpProber *prober.ICMPProber
	var err error
	if needV4ICMP || needV6ICMP {
		icmpProber, err = prober.NewICMPProber(cfg.Timeout, needV4ICMP, needV6ICMP)
		if err != nil {
			return err
		}
	}
	probers := prober.Set{
		TCP:  prober.NewTCPProber(cfg.Timeout),
		UDP:  prober.NewUDPProber(cfg.Timeout),
		ICMP: icmpProber,
	}
	states := make(map[string]*tracker.TargetState, len(allTargets))
	var controlKeys []string
	for _, t := range allTargets {
		state := &tracker.TargetState{Key: t.Key(), IsControl: t.IsControl}
		states[t.Key()] = state
		if t.IsControl {
			controlKeys = append(controlKeys, t.Key())
		}
	}
	notifier := webhook.NewNotifier(cfg.Webhooks, &http.Client{Timeout: 10 * time.Second})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	intervals := scheduler.IntervalConfig{Interval: cfg.Interval, BlockedCooldown: cfg.BlockedCooldown}
	var wg sync.WaitGroup
	for _, probeTarget := range allTargets {
		probeTarget := probeTarget
		state := states[probeTarget.Key()]
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastErr error
			check := func(ctx context.Context) bool {
				probeCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
				defer cancel()
				sample := probers.Probe(probeCtx, probeTarget)
				lastErr = sample.Err
				if sample.Success {
					log.Printf("probe success target=%s rtt=%s control=%t", probeTarget.Key(), sample.RTT, probeTarget.IsControl)
				} else {
					log.Printf("probe failed target=%s error=%v control=%t", probeTarget.Key(), sample.Err, probeTarget.IsControl)
				}
				return sample.Success
			}
			onResult := func(success bool) {
				changed, _, to := state.Record(success, cfg.Rise, cfg.Fall)
				if !changed || probeTarget.IsControl {
					return
				}
				event := makeEvent(probeTarget, state, to, lastErr, states, controlKeys)
				log.Printf("state changed target=%s event=%s", probeTarget.Key(), event.Event)
				notifier.Publish(event)
			}
			scheduler.MonitorTarget(ctx, state, intervals, check, onResult)
		}()
	}
	log.Printf("monitoring started targets=%d controls=%d webhooks=%d", len(targets), len(controls), len(cfg.Webhooks))
	<-ctx.Done()
	log.Printf("shutdown requested")
	wg.Wait()
	if !notifier.Close(5 * time.Second) {
		log.Printf("notification drain timed out")
	}
	return nil
}

func makeEvent(t target.Target, state *tracker.TargetState, next tracker.State, probeErr error, states map[string]*tracker.TargetState, controlKeys []string) webhook.Event {
	healthy, hasControl := tracker.ControlHealthy(states, controlKeys)
	eventName := "recovered"
	reason := "probe succeeded"
	if next == tracker.StateBlocked {
		eventName = "blocked"
		if hasControl && !healthy {
			eventName = "network_issue"
		}
		if probeErr != nil {
			reason = probeErr.Error()
		} else {
			reason = "probe failed"
		}
		if !hasControl {
			reason += "; no control targets configured, local network issues cannot be excluded"
		}
	}
	var controlOK *bool
	if hasControl {
		value := healthy
		controlOK = &value
	}
	_, _, failures := state.Snapshot()
	return webhook.Event{
		Version:             1,
		Event:               eventName,
		Timestamp:           time.Now().UTC().Format(time.RFC3339),
		IP:                  t.IP.String(),
		Protocol:            t.Kind.String(),
		Port:                t.Port,
		Reason:              reason,
		ControlOK:           controlOK,
		ConsecutiveFailures: failures,
	}
}
