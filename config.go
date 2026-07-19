package watchdog

import (
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"gfw-watchdog/internal/scheduler"
	"gfw-watchdog/internal/target"
	"gfw-watchdog/webhook"
)

type specList []target.Spec

func (l *specList) String() string { return "" }
func (l *specList) Set(s string) error {
	spec, err := target.ParseSpec(s)
	if err != nil {
		return err
	}
	*l = append(*l, spec)
	return nil
}

type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ";") }
func (l *stringList) Set(s string) error {
	*l = append(*l, s)
	return nil
}

type durationRangeValue struct {
	value scheduler.DurationRange
}

func (v *durationRangeValue) String() string {
	return fmt.Sprintf("%s-%s", v.value.Min, v.value.Max)
}
func (v *durationRangeValue) Set(s string) error {
	parsed, err := scheduler.ParseDurationRange(s)
	if err != nil {
		return err
	}
	v.value = parsed
	return nil
}

type Config struct {
	Targets         []target.Spec
	Controls        []target.Spec
	Interval        scheduler.DurationRange
	BlockedCooldown scheduler.DurationRange
	Rise            int
	Fall            int
	Timeout         time.Duration
	Webhooks        []webhook.Config
}

func ParseConfig(args []string, webhooksEnv string) (Config, error) {
	fs := flag.NewFlagSet("gfw-watchdog", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var targets, controls specList
	var webhookFlags stringList
	interval := durationRangeValue{value: scheduler.DurationRange{Min: 60 * time.Second, Max: 120 * time.Second}}
	cooldown := durationRangeValue{value: scheduler.DurationRange{Min: 12 * time.Hour, Max: 24 * time.Hour}}
	var cfg Config
	fs.Var(&targets, "ip", "probe target")
	fs.Var(&controls, "control", "control target")
	fs.Var(&interval, "interval", "normal interval range")
	fs.Var(&cooldown, "blocked-cooldown", "blocked cooldown range")
	fs.IntVar(&cfg.Rise, "rise", 1, "success threshold")
	fs.IntVar(&cfg.Fall, "fall", 3, "failure threshold")
	fs.DurationVar(&cfg.Timeout, "timeout", 5*time.Second, "probe timeout")
	fs.Var(&webhookFlags, "webhook", "notification target")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	if len(targets) == 0 {
		return Config{}, fmt.Errorf("at least one --ip is required")
	}
	if cfg.Rise < 1 || cfg.Fall < 1 {
		return Config{}, fmt.Errorf("--rise and --fall must be at least 1")
	}
	if cfg.Timeout <= 0 {
		return Config{}, fmt.Errorf("--timeout must be positive")
	}
	environmentWebhooks, err := ParseWebhooksEnv(webhooksEnv)
	if err != nil {
		return Config{}, fmt.Errorf("WEBHOOKS: %w", err)
	}
	cfg.Webhooks = environmentWebhooks
	for _, raw := range webhookFlags {
		parsed, err := ParseWebhook(raw)
		if err != nil {
			return Config{}, fmt.Errorf("--webhook: %w", err)
		}
		cfg.Webhooks = append(cfg.Webhooks, parsed)
	}
	cfg.Targets = targets
	cfg.Controls = controls
	cfg.Interval = interval.value
	cfg.BlockedCooldown = cooldown.value
	return cfg, nil
}

func TranslateShortArgs(in []string) []string {
	mapping := map[string]string{
		"i": "ip",
		"c": "control",
		"I": "interval",
		"b": "blocked-cooldown",
		"r": "rise",
		"f": "fall",
		"t": "timeout",
		"w": "webhook",
	}
	var out []string
	for idx := 0; idx < len(in); idx++ {
		a := in[idx]
		if a == "--" {
			return append(append(out, a), in[idx+1:]...)
		}
		if strings.HasPrefix(a, "--") || a == "-h" || !strings.HasPrefix(a, "-") || len(a) < 2 {
			out = append(out, a)
			continue
		}
		rest := a[1:]
		if eq := strings.Index(rest, "="); eq != -1 {
			if long, ok := mapping[rest[:eq]]; ok {
				out = append(out, "--"+long+"="+rest[eq+1:])
			} else {
				out = append(out, a)
			}
			continue
		}
		key, attached := rest[:1], rest[1:]
		long, ok := mapping[key]
		if !ok {
			out = append(out, a)
			continue
		}
		out = append(out, "--"+long)
		if attached != "" {
			out = append(out, attached)
		} else if idx+1 < len(in) && !strings.HasPrefix(in[idx+1], "-") {
			out = append(out, in[idx+1])
			idx++
		}
	}
	return out
}

func PrintUsage(w io.Writer) {
	const optionWidth = 33
	fmt.Fprintln(w, "Monitor IP reachability through TCP/UDP/ICMP, and report GFW blocking state changes.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gfw-watchdog --ip host[:item,...] [--ip ...] [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-i, --ip host[:item,...]", "Probe target (repeatable, required)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "", "Items: icmp, PORT, PORT/tcp, or PORT/udp")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-c, --control host[:item,...]", "Control target (repeatable)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-I, --interval min-max", "Normal probe interval (default 60s-120s)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-b, --blocked-cooldown min-max", "Blocked probe interval (default 12h-24h)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-r, --rise n", "Successes required for recovery (default 1)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-f, --fall n", "Failures required for blocking (default 3)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-t, --timeout duration", "Per-probe timeout (default 5s)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-w, --webhook spec", "Webhook target (repeatable)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "", "Format: type=telegram|wecom,url=URL[,name=NAME]")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-h, --help", "Show this help message and exit")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Environment variables:")
	fmt.Fprintln(w, "  WEBHOOKS  YAML-style list or newline-separated key=value webhook entries")
}
