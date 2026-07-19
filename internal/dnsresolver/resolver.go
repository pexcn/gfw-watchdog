package dnsresolver

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"strings"
	"time"

	"gfw-watchdog/internal/target"
	"golang.org/x/net/dns/dnsmessage"
)

const maxCNAMEHops = 8

type Result struct {
	IPs       []net.IP
	ExpiresAt time.Time
}

type Resolver struct {
	servers []string
	timeout time.Duration
}

func NewResolver(servers []string, timeout time.Duration) *Resolver {
	return &Resolver{servers: append([]string(nil), servers...), timeout: timeout}
}

func NewSystemResolver(timeout time.Duration) (*Resolver, error) {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil, fmt.Errorf("read /etc/resolv.conf: %w", err)
	}
	var servers []string
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			ip := net.ParseIP(strings.Trim(fields[1], "[]"))
			if ip != nil {
				servers = append(servers, net.JoinHostPort(ip.String(), "53"))
			}
		}
	}
	if len(servers) == 0 {
		return nil, errors.New("no nameserver found in /etc/resolv.conf")
	}
	return NewResolver(servers, timeout), nil
}

func (r *Resolver) Resolve(ctx context.Context, host string, family target.Family) (Result, error) {
	var recordType dnsmessage.Type
	switch family {
	case target.FamilyIPv4:
		recordType = dnsmessage.TypeA
	case target.FamilyIPv6:
		recordType = dnsmessage.TypeAAAA
	default:
		return Result{}, fmt.Errorf("DNS family must be ipv4 or ipv6")
	}
	name, err := dnsmessage.NewName(ensureFQDN(host))
	if err != nil {
		return Result{}, fmt.Errorf("invalid DNS name %q: %w", host, err)
	}
	var minimumTTL uint32
	hasTTL := false
	for hop := 0; hop <= maxCNAMEHops; hop++ {
		message, err := r.query(ctx, name, recordType)
		if err != nil {
			return Result{}, err
		}
		var ips []net.IP
		var cname *dnsmessage.Name
		for _, answer := range message.Answers {
			switch body := answer.Body.(type) {
			case *dnsmessage.AResource:
				if recordType == dnsmessage.TypeA {
					ips = append(ips, net.IPv4(body.A[0], body.A[1], body.A[2], body.A[3]))
					minimumTTL, hasTTL = minTTL(minimumTTL, hasTTL, answer.Header.TTL)
				}
			case *dnsmessage.AAAAResource:
				if recordType == dnsmessage.TypeAAAA {
					ip := make(net.IP, net.IPv6len)
					copy(ip, body.AAAA[:])
					ips = append(ips, ip)
					minimumTTL, hasTTL = minTTL(minimumTTL, hasTTL, answer.Header.TTL)
				}
			case *dnsmessage.CNAMEResource:
				value := body.CNAME
				cname = &value
				minimumTTL, hasTTL = minTTL(minimumTTL, hasTTL, answer.Header.TTL)
			}
		}
		if len(ips) > 0 {
			return Result{IPs: deduplicate(ips), ExpiresAt: time.Now().Add(time.Duration(minimumTTL) * time.Second)}, nil
		}
		if cname == nil {
			negativeTTL := negativeTTL(message)
			return Result{ExpiresAt: time.Now().Add(time.Duration(negativeTTL) * time.Second)}, nil
		}
		name = *cname
	}
	return Result{}, fmt.Errorf("CNAME chain for %s exceeds %d hops", host, maxCNAMEHops)
}

func (r *Resolver) query(ctx context.Context, name dnsmessage.Name, recordType dnsmessage.Type) (dnsmessage.Message, error) {
	id := uint16(rand.Uint32())
	request := dnsmessage.Message{
		Header:    dnsmessage.Header{ID: id, RecursionDesired: true},
		Questions: []dnsmessage.Question{{Name: name, Type: recordType, Class: dnsmessage.ClassINET}},
	}
	payload, err := request.Pack()
	if err != nil {
		return dnsmessage.Message{}, err
	}
	var errs []error
	for _, server := range r.servers {
		response, err := r.exchange(ctx, "udp", server, payload)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", server, err))
			continue
		}
		message, err := unpackResponse(response, id)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", server, err))
			continue
		}
		if message.Header.Truncated {
			response, err = r.exchange(ctx, "tcp", server, payload)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s TCP fallback: %w", server, err))
				continue
			}
			message, err = unpackResponse(response, id)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s TCP fallback: %w", server, err))
				continue
			}
		}
		if message.Header.RCode != dnsmessage.RCodeSuccess {
			return dnsmessage.Message{}, fmt.Errorf("DNS response code %s for %s", message.Header.RCode, name.String())
		}
		return message, nil
	}
	return dnsmessage.Message{}, errors.Join(errs...)
}

func (r *Resolver) exchange(ctx context.Context, network, server string, payload []byte) ([]byte, error) {
	dialer := net.Dialer{Timeout: r.timeout}
	conn, err := dialer.DialContext(ctx, network, server)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(r.timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	if err := conn.SetDeadline(deadline); err != nil {
		return nil, err
	}
	if network == "tcp" {
		framed := make([]byte, len(payload)+2)
		binary.BigEndian.PutUint16(framed, uint16(len(payload)))
		copy(framed[2:], payload)
		if _, err := conn.Write(framed); err != nil {
			return nil, err
		}
		header := make([]byte, 2)
		if _, err := ioReadFull(conn, header); err != nil {
			return nil, err
		}
		response := make([]byte, binary.BigEndian.Uint16(header))
		_, err := ioReadFull(conn, response)
		return response, err
	}
	if _, err := conn.Write(payload); err != nil {
		return nil, err
	}
	response := make([]byte, 64*1024)
	n, err := conn.Read(response)
	return response[:n], err
}

func unpackResponse(payload []byte, id uint16) (dnsmessage.Message, error) {
	var message dnsmessage.Message
	if err := message.Unpack(payload); err != nil {
		return message, err
	}
	if !message.Header.Response || message.Header.ID != id {
		return message, errors.New("invalid DNS response")
	}
	return message, nil
}

func ensureFQDN(host string) string {
	if strings.HasSuffix(host, ".") {
		return host
	}
	return host + "."
}

func minTTL(current uint32, initialized bool, candidate uint32) (uint32, bool) {
	if !initialized || candidate < current {
		return candidate, true
	}
	return current, true
}

func deduplicate(ips []net.IP) []net.IP {
	seen := make(map[string]bool)
	result := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		key := ip.String()
		if !seen[key] {
			result = append(result, ip)
			seen[key] = true
		}
	}
	return result
}

func negativeTTL(message dnsmessage.Message) uint32 {
	const defaultNegativeTTL = 60
	var ttl uint32
	initialized := false
	for _, authority := range message.Authorities {
		soa, ok := authority.Body.(*dnsmessage.SOAResource)
		if !ok {
			continue
		}
		candidate := authority.Header.TTL
		if soa.MinTTL < candidate {
			candidate = soa.MinTTL
		}
		ttl, initialized = minTTL(ttl, initialized, candidate)
	}
	if !initialized {
		return defaultNegativeTTL
	}
	return ttl
}

func ioReadFull(conn net.Conn, buffer []byte) (int, error) {
	total := 0
	for total < len(buffer) {
		n, err := conn.Read(buffer[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
