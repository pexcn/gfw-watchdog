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

type Family int

const (
	FamilyAny Family = iota
	FamilyIPv4
	FamilyIPv6
)

type Spec struct {
	Host   string
	IP     net.IP
	Family Family
	Items  []ProbeItem
}

type Target struct {
	Host      string
	IP        net.IP
	Kind      ProbeKind
	Port      int
	IsControl bool
}

func (t Target) Key() string {
	return fmt.Sprintf("%t|%s|%s|%s|%d", t.IsControl, t.Host, t.IP.String(), t.Kind.String(), t.Port)
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
	hostPart, itemsStr := s, ""
	if index := strings.IndexByte(s, ':'); index >= 0 {
		if strings.IndexByte(s[index+1:], ':') >= 0 {
			return Spec{}, fmt.Errorf("invalid spec: %s", s)
		}
		hostPart, itemsStr = s[:index], s[index+1:]
	}
	if ip := net.ParseIP(hostPart); ip != nil {
		items, err := ParseItems(itemsStr)
		if err != nil {
			return Spec{}, err
		}
		return Spec{IP: ip, Items: items}, nil
	}
	host, family, err := parseDomainHost(hostPart)
	if err != nil {
		return Spec{}, err
	}
	items := []ProbeItem{{Kind: ProbeICMP}}
	if strings.Contains(s, ":") {
		items, err = ParseItems(itemsStr)
		if err != nil {
			return Spec{}, err
		}
	}
	return Spec{Host: host, Family: family, Items: items}, nil
}

func parseDomainHost(raw string) (string, Family, error) {
	host := strings.ToLower(strings.TrimSuffix(raw, "."))
	family := FamilyAny
	if base, suffix, ok := strings.Cut(host, "@"); ok {
		host = base
		switch suffix {
		case "ipv4":
			family = FamilyIPv4
		case "ipv6":
			family = FamilyIPv6
		default:
			return "", FamilyAny, fmt.Errorf("invalid address family %q, want ipv4 or ipv6", suffix)
		}
	}
	if !validDomain(host) {
		return "", FamilyAny, fmt.Errorf("invalid host: %s", raw)
	}
	return host, family, nil
}

func validDomain(host string) bool {
	if len(host) == 0 || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
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
		if spec.IP == nil {
			continue
		}
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

func ExpandResolved(spec Spec, ips []net.IP, control bool) []Target {
	var result []Target
	for _, ip := range ips {
		for _, item := range spec.Items {
			result = append(result, Target{Host: spec.Host, IP: ip, Kind: item.Kind, Port: item.Port, IsControl: control})
		}
	}
	return result
}
