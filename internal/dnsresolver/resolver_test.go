package dnsresolver

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"gfw-watchdog/internal/target"
	"golang.org/x/net/dns/dnsmessage"
)

func TestResolveFamiliesAndMinimumTTL(t *testing.T) {
	server := startDNSServer(t, func(request dnsmessage.Message, tcp bool) dnsmessage.Message {
		question := request.Questions[0]
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.Header.ID, Response: true, RecursionAvailable: true}, Questions: request.Questions}
		switch question.Type {
		case dnsmessage.TypeA:
			response.Answers = []dnsmessage.Resource{
				{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 120}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}},
				{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 2}}},
			}
		case dnsmessage.TypeAAAA:
			response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeAAAA, Class: dnsmessage.ClassINET, TTL: 300}, Body: &dnsmessage.AAAAResource{AAAA: [16]byte{0x20, 0x01, 0x0d, 0xb8, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}}}}
		}
		return response
	})
	resolver := NewResolver([]string{server}, time.Second)
	before := time.Now()
	v4, err := resolver.Resolve(context.Background(), "example.com", target.FamilyIPv4)
	if err != nil {
		t.Fatal(err)
	}
	if len(v4.IPs) != 2 || v4.ExpiresAt.Before(before.Add(59*time.Second)) || v4.ExpiresAt.After(before.Add(61*time.Second)) {
		t.Fatalf("unexpected IPv4 result: %#v", v4)
	}
	v6, err := resolver.Resolve(context.Background(), "example.com", target.FamilyIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if len(v6.IPs) != 1 || v6.IPs[0].To4() != nil {
		t.Fatalf("unexpected IPv6 result: %#v", v6)
	}
}

func TestResolveCNAMEUsesMinimumTTL(t *testing.T) {
	alias, _ := dnsmessage.NewName("alias.example.com.")
	server := startDNSServer(t, func(request dnsmessage.Message, tcp bool) dnsmessage.Message {
		question := request.Questions[0]
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.Header.ID, Response: true}, Questions: request.Questions}
		if question.Name.String() == "example.com." {
			response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET, TTL: 30}, Body: &dnsmessage.CNAMEResource{CNAME: alias}}}
		} else {
			response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 300}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 1}}}}
		}
		return response
	})
	resolver := NewResolver([]string{server}, time.Second)
	before := time.Now()
	result, err := resolver.Resolve(context.Background(), "example.com", target.FamilyIPv4)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpiresAt.Before(before.Add(29*time.Second)) || result.ExpiresAt.After(before.Add(31*time.Second)) {
		t.Fatalf("CNAME TTL not applied: %s", result.ExpiresAt.Sub(before))
	}
}

func TestResolveEmptyAnswerUsesSOANegativeTTL(t *testing.T) {
	zone, _ := dnsmessage.NewName("example.com.")
	ns, _ := dnsmessage.NewName("ns.example.com.")
	mailbox, _ := dnsmessage.NewName("hostmaster.example.com.")
	server := startDNSServer(t, func(request dnsmessage.Message, tcp bool) dnsmessage.Message {
		return dnsmessage.Message{
			Header:    dnsmessage.Header{ID: request.Header.ID, Response: true},
			Questions: request.Questions,
			Authorities: []dnsmessage.Resource{{
				Header: dnsmessage.ResourceHeader{Name: zone, Type: dnsmessage.TypeSOA, Class: dnsmessage.ClassINET, TTL: 120},
				Body:   &dnsmessage.SOAResource{NS: ns, MBox: mailbox, MinTTL: 45},
			}},
		}
	})
	resolver := NewResolver([]string{server}, time.Second)
	before := time.Now()
	result, err := resolver.Resolve(context.Background(), "example.com", target.FamilyIPv6)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.IPs) != 0 || result.ExpiresAt.Before(before.Add(44*time.Second)) || result.ExpiresAt.After(before.Add(46*time.Second)) {
		t.Fatalf("unexpected negative result: %#v", result)
	}
}

func startDNSServer(t *testing.T, handler func(dnsmessage.Message, bool) dnsmessage.Message) string {
	t.Helper()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	udp, err := net.ListenPacket("udp", tcp.Addr().String())
	if err != nil {
		tcp.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { tcp.Close(); udp.Close() })
	go func() {
		buffer := make([]byte, 64*1024)
		for {
			n, remote, err := udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buffer[:n]) != nil {
				continue
			}
			response := handler(request, false)
			payload, _ := response.Pack()
			udp.WriteTo(payload, remote)
		}
	}()
	go func() {
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				header := make([]byte, 2)
				if _, err := io.ReadFull(conn, header); err != nil {
					return
				}
				payload := make([]byte, binary.BigEndian.Uint16(header))
				if _, err := io.ReadFull(conn, payload); err != nil {
					return
				}
				var request dnsmessage.Message
				if request.Unpack(payload) != nil {
					return
				}
				response := handler(request, true)
				packed, _ := response.Pack()
				binary.BigEndian.PutUint16(header, uint16(len(packed)))
				conn.Write(append(header, packed...))
			}()
		}
	}()
	return tcp.Addr().String()
}
