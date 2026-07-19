package prober

import (
	"context"
	"fmt"
	"time"

	"gfw-watchdog/internal/target"
)

type Sample struct {
	Success bool
	RTT     time.Duration
	Err     error
}

type Prober interface {
	Probe(context.Context, target.Target) Sample
}

type Set struct {
	TCP  *TCPProber
	UDP  *UDPProber
	ICMP *ICMPProber
}

func (s Set) Probe(ctx context.Context, t target.Target) Sample {
	switch t.Kind {
	case target.ProbeTCP:
		return s.TCP.Probe(ctx, t)
	case target.ProbeUDP:
		return s.UDP.Probe(ctx, t)
	case target.ProbeICMP:
		if s.ICMP == nil {
			return Sample{Err: fmt.Errorf("ICMP prober is not initialized")}
		}
		return s.ICMP.Probe(ctx, t)
	default:
		return Sample{Err: fmt.Errorf("unsupported probe kind %d", t.Kind)}
	}
}
