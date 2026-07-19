package target

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

type ProbeKind int

const (
	ProbeICMP ProbeKind = iota
	ProbeTCP
	ProbeUDP
)

func (k ProbeKind) String() string {
	switch k {
	case ProbeICMP:
		return "icmp"
	case ProbeTCP:
		return "tcp"
	case ProbeUDP:
		return "udp"
	default:
		return "unknown"
	}
}

type ProbeItem struct {
	Kind ProbeKind
	Port int
}

type Spec struct {
	IP    net.IP
	Items []ProbeItem
}

type Target struct {
	IP        net.IP
	Kind      ProbeKind
	Port      int
	IsControl bool
}

func (t Target) Key() string {
	return fmt.Sprintf("%s|%s|%d", t.IP.String(), t.Kind.String(), t.Port)
}

func ParseSpec(s string) (Spec, error) {
	if strings.HasPrefix(s, "[") {
		idx := strings.Index(s, "]")
		if idx == -1 {
			return Spec{}, fmt.Errorf("invalid spec: %s", s)
		}
		ip := net.ParseIP(s[1:idx])
		if ip == nil || ip.To4() != nil {
			return Spec{}, fmt.Errorf("invalid ipv6: %s", s[1:idx])
		}
		rest := s[idx+1:]
		if strings.HasPrefix(rest, ":") {
			items, err := ParseItems(rest[1:])
			if err != nil {
				return Spec{}, err
			}
			return Spec{IP: ip, Items: items}, nil
		}
		if rest != "" {
			return Spec{}, fmt.Errorf("invalid spec: %s", s)
		}
		return Spec{IP: ip, Items: []ProbeItem{{Kind: ProbeICMP}}}, nil
	}
	if ip := net.ParseIP(s); ip != nil {
		return Spec{IP: ip, Items: []ProbeItem{{Kind: ProbeICMP}}}, nil
	}
	host, itemsStr, err := net.SplitHostPort(s)
	if err != nil {
		return Spec{}, fmt.Errorf("invalid spec: %s", s)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return Spec{}, fmt.Errorf("invalid ip: %s", host)
	}
	items, err := ParseItems(itemsStr)
	if err != nil {
		return Spec{}, err
	}
	return Spec{IP: ip, Items: items}, nil
}

func ParseItems(s string) ([]ProbeItem, error) {
	if s == "" {
		return nil, fmt.Errorf("empty item list")
	}
	var items []ProbeItem
	seen := make(map[ProbeItem]bool)
	for _, token := range strings.Split(s, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			return nil, fmt.Errorf("empty item in %q", s)
		}
		var item ProbeItem
		if token == "icmp" {
			item = ProbeItem{Kind: ProbeICMP}
		} else {
			portString, protocol := token, "tcp"
			if i := strings.Index(token, "/"); i != -1 {
				portString, protocol = token[:i], token[i+1:]
			}
			port, err := strconv.Atoi(portString)
			if err != nil || port < 1 || port > 65535 {
				return nil, fmt.Errorf("invalid port in item %q", token)
			}
			switch protocol {
			case "tcp":
				item = ProbeItem{Kind: ProbeTCP, Port: port}
			case "udp":
				item = ProbeItem{Kind: ProbeUDP, Port: port}
			default:
				return nil, fmt.Errorf("invalid protocol %q in item %q, want tcp or udp", protocol, token)
			}
		}
		if !seen[item] {
			items = append(items, item)
			seen[item] = true
		}
	}
	return items, nil
}

func Expand(specs []Spec, control bool) []Target {
	var result []Target
	seen := make(map[string]bool)
	for _, spec := range specs {
		for _, item := range spec.Items {
			t := Target{IP: spec.IP, Kind: item.Kind, Port: item.Port, IsControl: control}
			if !seen[t.Key()] {
				result = append(result, t)
				seen[t.Key()] = true
			}
		}
	}
	return result
}
