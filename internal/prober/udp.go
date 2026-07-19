package prober

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strconv"
	"time"

	"gfw-watchdog/internal/target"
)

type UDPProber struct {
	timeout time.Duration
}

func NewUDPProber(timeout time.Duration) *UDPProber {
	return &UDPProber{timeout: timeout}
}

func (p *UDPProber) Probe(ctx context.Context, t target.Target) Sample {
	start := time.Now()
	dialer := net.Dialer{Timeout: p.timeout}
	conn, err := dialer.DialContext(ctx, "udp", net.JoinHostPort(t.IP.String(), strconv.Itoa(t.Port)))
	if err != nil {
		return Sample{Err: err}
	}
	defer conn.Close()
	deadline := time.Now().Add(p.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return Sample{Err: err}
	}
	payload := make([]byte, 8)
	if _, err := rand.Read(payload); err != nil {
		return Sample{Err: err}
	}
	if _, err := conn.Write(payload); err != nil {
		return Sample{Err: err}
	}
	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return Sample{Err: err}
	}
	if !bytes.Equal(buffer[:n], payload) {
		return Sample{Err: errors.New("echo mismatch")}
	}
	return Sample{Success: true, RTT: time.Since(start)}
}
