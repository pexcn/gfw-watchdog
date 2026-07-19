package prober

import (
	"context"
	"net"
	"strconv"
	"time"

	"gfw-watchdog/internal/target"
)

type TCPProber struct {
	timeout time.Duration
}

func NewTCPProber(timeout time.Duration) *TCPProber {
	return &TCPProber{timeout: timeout}
}

func (p *TCPProber) Probe(ctx context.Context, t target.Target) Sample {
	start := time.Now()
	dialer := net.Dialer{Timeout: p.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(t.IP.String(), strconv.Itoa(t.Port)))
	if err != nil {
		return Sample{Err: err}
	}
	defer conn.Close()
	return Sample{Success: true, RTT: time.Since(start)}
}
