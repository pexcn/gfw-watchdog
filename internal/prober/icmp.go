package prober

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"gfw-watchdog/internal/target"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type icmpMode struct {
	network string
	listen  string
	proto   int
	typeReq icmp.Type
	typeRep icmp.Type
	udp     bool
}

type ICMPProber struct {
	timeout time.Duration
	v4      *icmpMode
	v6      *icmpMode
	seq     atomic.Uint32
}

func NewICMPProber(timeout time.Duration, needV4, needV6 bool) (*ICMPProber, error) {
	p := &ICMPProber{timeout: timeout}
	var err error
	if needV4 {
		p.v4, err = detectICMPMode(false)
		if err != nil {
			return nil, fmt.Errorf("IPv4 ICMP unavailable: %w; adjust net.ipv4.ping_group_range or grant CAP_NET_RAW", err)
		}
	}
	if needV6 {
		p.v6, err = detectICMPMode(true)
		if err != nil {
			return nil, fmt.Errorf("IPv6 ICMP unavailable: %w; grant CAP_NET_RAW if unprivileged ping sockets are unavailable", err)
		}
	}
	return p, nil
}

func detectICMPMode(v6 bool) (*icmpMode, error) {
	modes := []icmpMode{
		{network: "udp4", listen: "0.0.0.0", proto: 1, typeReq: ipv4.ICMPTypeEcho, typeRep: ipv4.ICMPTypeEchoReply, udp: true},
		{network: "ip4:icmp", listen: "0.0.0.0", proto: 1, typeReq: ipv4.ICMPTypeEcho, typeRep: ipv4.ICMPTypeEchoReply},
	}
	if v6 {
		modes = []icmpMode{
			{network: "udp6", listen: "::", proto: 58, typeReq: ipv6.ICMPTypeEchoRequest, typeRep: ipv6.ICMPTypeEchoReply, udp: true},
			{network: "ip6:ipv6-icmp", listen: "::", proto: 58, typeReq: ipv6.ICMPTypeEchoRequest, typeRep: ipv6.ICMPTypeEchoReply},
		}
	}
	var errs []error
	for i := range modes {
		conn, err := icmp.ListenPacket(modes[i].network, modes[i].listen)
		if err == nil {
			conn.Close()
			return &modes[i], nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", modes[i].network, err))
	}
	return nil, errors.Join(errs...)
}

func (p *ICMPProber) Probe(ctx context.Context, t target.Target) Sample {
	mode := p.v6
	if t.IP.To4() != nil {
		mode = p.v4
	}
	if mode == nil {
		return Sample{Err: errors.New("ICMP address family was not initialized")}
	}
	conn, err := icmp.ListenPacket(mode.network, mode.listen)
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
	sequence := int(p.seq.Add(1) & 0xffff)
	id := os.Getpid() & 0xffff
	message := icmp.Message{Type: mode.typeReq, Code: 0, Body: &icmp.Echo{ID: id, Seq: sequence, Data: []byte("gfw-watchdog")}}
	data, err := message.Marshal(nil)
	if err != nil {
		return Sample{Err: err}
	}
	var destination net.Addr = &net.IPAddr{IP: t.IP}
	if mode.udp {
		destination = &net.UDPAddr{IP: t.IP}
	}
	start := time.Now()
	if _, err := conn.WriteTo(data, destination); err != nil {
		return Sample{Err: err}
	}
	buffer := make([]byte, 1500)
	for {
		n, _, err := conn.ReadFrom(buffer)
		if err != nil {
			return Sample{Err: err}
		}
		reply, err := icmp.ParseMessage(mode.proto, buffer[:n])
		if err != nil || reply.Type != mode.typeRep {
			continue
		}
		echo, ok := reply.Body.(*icmp.Echo)
		if ok && echo.Seq == sequence && (mode.udp || echo.ID == id) {
			return Sample{Success: true, RTT: time.Since(start)}
		}
	}
}
