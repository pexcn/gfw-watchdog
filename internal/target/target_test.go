package target

import "testing"

func TestParseSpec(t *testing.T) {
	tests := []struct {
		input  string
		host   string
		family Family
		want   []ProbeItem
	}{
		{"1.2.3.4", "", FamilyAny, []ProbeItem{{Kind: ProbeICMP}}},
		{"2001:db8::1", "", FamilyAny, []ProbeItem{{Kind: ProbeICMP}}},
		{"1.2.3.4:80", "", FamilyAny, []ProbeItem{{Kind: ProbeTCP, Port: 80}}},
		{"1.2.3.4:80/tcp,80/udp,icmp,80/tcp", "", FamilyAny, []ProbeItem{{Kind: ProbeTCP, Port: 80}, {Kind: ProbeUDP, Port: 80}, {Kind: ProbeICMP}}},
		{"[2001:db8::1]:443/tcp,53/udp", "", FamilyAny, []ProbeItem{{Kind: ProbeTCP, Port: 443}, {Kind: ProbeUDP, Port: 53}}},
		{"Example.COM.:443/tcp", "example.com", FamilyAny, []ProbeItem{{Kind: ProbeTCP, Port: 443}}},
		{"example.com@ipv4:443/tcp", "example.com", FamilyIPv4, []ProbeItem{{Kind: ProbeTCP, Port: 443}}},
		{"example.com@ipv6", "example.com", FamilyIPv6, []ProbeItem{{Kind: ProbeICMP}}},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := ParseSpec(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if got.Host != test.host || got.Family != test.family {
				t.Fatalf("got host=%q family=%v, want host=%q family=%v", got.Host, got.Family, test.host, test.family)
			}
			if len(got.Items) != len(test.want) {
				t.Fatalf("got %v, want %v", got.Items, test.want)
			}
			for i := range test.want {
				if got.Items[i] != test.want[i] {
					t.Fatalf("got %v, want %v", got.Items, test.want)
				}
			}
		})
	}
}

func TestParseSpecErrors(t *testing.T) {
	for _, input := range []string{"", "localhost:80", "example.com@v4:80", "example.com@:80", "1.2.3.4:", "1.2.3.4:0", "1.2.3.4:53/quic", "[1.2.3.4]:80", "[2001:db8::1"} {
		if _, err := ParseSpec(input); err == nil {
			t.Errorf("ParseSpec(%q) unexpectedly succeeded", input)
		}
	}
}
