package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	watchdog "gfw-watchdog"
	"gfw-watchdog/internal/dnsresolver"
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
	needV4ICMP, needV6ICMP := false, false
	allSpecs := append(append([]target.Spec{}, cfg.Targets...), cfg.Controls...)
	for _, spec := range allSpecs {
		for _, item := range spec.Items {
			if item.Kind != target.ProbeICMP {
				continue
			}
			if spec.IP != nil {
				if spec.IP.To4() != nil {
					needV4ICMP = true
				} else {
					needV6ICMP = true
				}
			} else {
				needV4ICMP = needV4ICMP || spec.Family != target.FamilyIPv6
				needV6ICMP = needV6ICMP || spec.Family != target.FamilyIPv4
			}
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
	notifier := webhook.NewNotifier(cfg.Webhooks, &http.Client{Timeout: 10 * time.Second})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	intervals := scheduler.IntervalConfig{Interval: cfg.Interval, BlockedCooldown: cfg.BlockedCooldown}
	registry := newMonitorRegistry(ctx, cfg, intervals, probers, notifier)
	for _, t := range append(target.Expand(cfg.Targets, false), target.Expand(cfg.Controls, true)...) {
		registry.add(t, nil)
	}
	resolver, err := dnsresolver.NewSystemResolver(cfg.Timeout)
	if err != nil && hasDomain(allSpecs) {
		return err
	}
	var groups []*domainGroup
	groupSpecs := make(map[string]target.Spec)
	groupControls := make(map[string]bool)
	groupRequired := make(map[string]bool)
	for _, entry := range []struct {
		specs   []target.Spec
		control bool
	}{{cfg.Targets, false}, {cfg.Controls, true}} {
		for _, spec := range entry.specs {
			if spec.IP != nil {
				continue
			}
			families := []target.Family{spec.Family}
			if spec.Family == target.FamilyAny {
				families = []target.Family{target.FamilyIPv4, target.FamilyIPv6}
			}
			for _, family := range families {
				key := fmt.Sprintf("%t|%s|%d", entry.control, spec.Host, family)
				merged := groupSpecs[key]
				merged.Host = spec.Host
				merged.Family = family
				merged.Items = mergeItems(merged.Items, spec.Items)
				groupSpecs[key] = merged
				groupControls[key] = entry.control
				groupRequired[key] = groupRequired[key] || spec.Family != target.FamilyAny
			}
		}
	}
	for key, spec := range groupSpecs {
		group := &domainGroup{spec: spec, family: spec.Family, control: groupControls[key], resolver: resolver, registry: registry}
		if err := group.refresh(ctx); err != nil {
			if groupRequired[key] {
				return fmt.Errorf("resolve %s@%s: %w", spec.Host, familyName(spec.Family), err)
			}
			log.Printf("DNS initial lookup failed host=%s family=%s error=%v", spec.Host, familyName(spec.Family), err)
		}
		groups = append(groups, group)
	}
	for _, group := range groups {
		for _, sibling := range groups {
			if sibling.control == group.control && sibling.spec.Host == group.spec.Host {
				group.siblings = append(group.siblings, sibling)
			}
		}
	}
	for _, spec := range append(cfg.Targets, cfg.Controls...) {
		if spec.Host != "" && spec.Family == target.FamilyAny && !hasResolvedGroup(groups, spec.Host) {
			return fmt.Errorf("resolve %s: neither A nor AAAA records are available", spec.Host)
		}
	}
	ordinary, controls := registry.counts()
	log.Printf("monitoring started targets=%d controls=%d webhooks=%d", ordinary, controls, len(cfg.Webhooks))
	<-ctx.Done()
	log.Printf("shutdown requested")
	registry.close()
	if !notifier.Close(5 * time.Second) {
		log.Printf("notification drain timed out")
	}
	return nil
}

type monitoredTarget struct {
	target target.Target
	state  *tracker.TargetState
	cancel context.CancelFunc
	group  *domainGroup
}

type monitorRegistry struct {
	mu        sync.RWMutex
	ctx       context.Context
	cfg       watchdog.Config
	intervals scheduler.IntervalConfig
	probers   prober.Set
	notifier  *webhook.Notifier
	targets   map[string]*monitoredTarget
	wg        sync.WaitGroup
	closed    bool
}

func newMonitorRegistry(ctx context.Context, cfg watchdog.Config, intervals scheduler.IntervalConfig, probers prober.Set, notifier *webhook.Notifier) *monitorRegistry {
	return &monitorRegistry{ctx: ctx, cfg: cfg, intervals: intervals, probers: probers, notifier: notifier, targets: make(map[string]*monitoredTarget)}
}

func (r *monitorRegistry) add(t target.Target, group *domainGroup) {
	r.mu.Lock()
	if r.closed || r.targets[t.Key()] != nil {
		r.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(r.ctx)
	monitored := &monitoredTarget{target: t, state: &tracker.TargetState{Key: t.Key(), IsControl: t.IsControl}, cancel: cancel, group: group}
	r.targets[t.Key()] = monitored
	r.wg.Add(1)
	r.mu.Unlock()
	go r.monitor(ctx, monitored)
}

func (r *monitorRegistry) remove(key string) {
	r.mu.Lock()
	monitored := r.targets[key]
	if monitored != nil {
		delete(r.targets, key)
		monitored.cancel()
	}
	r.mu.Unlock()
}

func (r *monitorRegistry) monitor(ctx context.Context, monitored *monitoredTarget) {
	defer r.wg.Done()
	var lastErr error
	check := func(ctx context.Context) (bool, bool) {
		if monitored.group != nil {
			monitored.group.ensureFresh(ctx)
			if !r.active(monitored.target.Key(), monitored) {
				return false, false
			}
		}
		probeCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
		defer cancel()
		sample := r.probers.Probe(probeCtx, monitored.target)
		lastErr = sample.Err
		if sample.Success {
			log.Printf("probe success %s rtt=%s", formatTargetLog(monitored.target), sample.RTT)
		} else {
			log.Printf("probe failed %s error=%v", formatTargetLog(monitored.target), sample.Err)
		}
		return sample.Success, ctx.Err() == nil
	}
	onResult := func(success bool) {
		if !r.active(monitored.target.Key(), monitored) {
			return
		}
		changed, _, to := monitored.state.Record(success, r.cfg.Rise, r.cfg.Fall)
		if !changed || monitored.target.IsControl {
			return
		}
		states, controlKeys := r.snapshot(monitored.target.IP.To4() == nil)
		event := makeEvent(monitored.target, monitored.state, to, lastErr, states, controlKeys)
		log.Printf("state changed %s event=%s", formatTargetLog(monitored.target), event.Event)
		r.notifier.Publish(event)
	}
	scheduler.MonitorTarget(ctx, monitored.state, r.intervals, check, onResult)
}

func (r *monitorRegistry) active(key string, monitored *monitoredTarget) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.targets[key] == monitored
}

func (r *monitorRegistry) snapshot(ipv6 bool) (map[string]*tracker.TargetState, []string) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	states := make(map[string]*tracker.TargetState, len(r.targets))
	var controls []string
	for key, monitored := range r.targets {
		states[key] = monitored.state
		if monitored.target.IsControl && (monitored.target.IP.To4() == nil) == ipv6 {
			controls = append(controls, key)
		}
	}
	return states, controls
}

func (r *monitorRegistry) counts() (ordinary, controls int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, monitored := range r.targets {
		if monitored.target.IsControl {
			controls++
		} else {
			ordinary++
		}
	}
	return ordinary, controls
}

func (r *monitorRegistry) close() {
	r.mu.Lock()
	r.closed = true
	for _, monitored := range r.targets {
		monitored.cancel()
	}
	r.mu.Unlock()
	r.wg.Wait()
}

type domainGroup struct {
	mu        sync.Mutex
	spec      target.Spec
	family    target.Family
	control   bool
	resolver  *dnsresolver.Resolver
	registry  *monitorRegistry
	expiresAt time.Time
	keys      map[string]bool
	siblings  []*domainGroup
}

func (g *domainGroup) ensureFresh(ctx context.Context) {
	for _, sibling := range g.siblings {
		sibling.ensureOwnFresh(ctx)
	}
}

func (g *domainGroup) ensureOwnFresh(ctx context.Context) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if time.Now().Before(g.expiresAt) {
		return
	}
	if err := g.refreshLocked(ctx); err != nil {
		log.Printf("DNS refresh failed host=%s family=%s error=%v", g.spec.Host, familyName(g.family), err)
		g.expiresAt = time.Now().Add(time.Minute)
	}
}

func (g *domainGroup) refresh(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.refreshLocked(ctx)
}

func (g *domainGroup) refreshLocked(ctx context.Context) error {
	result, err := g.resolver.Resolve(ctx, g.spec.Host, g.family)
	if err != nil {
		return err
	}
	newKeys := make(map[string]bool)
	for _, t := range target.ExpandResolved(g.spec, result.IPs, g.control) {
		newKeys[t.Key()] = true
		g.registry.add(t, g)
	}
	for key := range g.keys {
		if !newKeys[key] {
			g.registry.remove(key)
		}
	}
	g.keys = newKeys
	g.expiresAt = result.ExpiresAt
	log.Printf("DNS refreshed host=%s family=%s addresses=%d expires=%s", g.spec.Host, familyName(g.family), len(result.IPs), result.ExpiresAt.Format(time.RFC3339))
	return nil
}

func hasDomain(specs []target.Spec) bool {
	for _, spec := range specs {
		if spec.Host != "" {
			return true
		}
	}
	return false
}

func hasResolvedGroup(groups []*domainGroup, host string) bool {
	for _, group := range groups {
		group.mu.Lock()
		resolved := group.spec.Host == host && len(group.keys) > 0
		group.mu.Unlock()
		if resolved {
			return true
		}
	}
	return false
}

func familyName(family target.Family) string {
	if family == target.FamilyIPv4 {
		return "ipv4"
	}
	return "ipv6"
}

func mergeItems(existing, added []target.ProbeItem) []target.ProbeItem {
	seen := make(map[target.ProbeItem]bool, len(existing)+len(added))
	result := append([]target.ProbeItem(nil), existing...)
	for _, item := range existing {
		seen[item] = true
	}
	for _, item := range added {
		if !seen[item] {
			result = append(result, item)
			seen[item] = true
		}
	}
	return result
}

func formatTargetLog(t target.Target) string {
	var fields []string
	if t.Host != "" {
		fields = append(fields, "host="+t.Host)
	}
	fields = append(fields, "ip="+t.IP.String(), "protocol="+t.Kind.String())
	if t.Port > 0 {
		fields = append(fields, fmt.Sprintf("port=%d", t.Port))
	}
	fields = append(fields, fmt.Sprintf("control=%t", t.IsControl))
	return strings.Join(fields, " ")
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
		Host:                t.Host,
		Protocol:            t.Kind.String(),
		Port:                t.Port,
		Reason:              reason,
		ControlOK:           controlOK,
		ConsecutiveFailures: failures,
	}
}
