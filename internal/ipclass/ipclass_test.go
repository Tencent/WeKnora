package ipclass

import (
	"net"
	"testing"
)

func TestClassify(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		ip    string
		class Class
	}{
		{"IPv4 public", "8.8.8.8", Public},
		{"IPv6 public", "2001:4860:4860::8888", Public},
		{"IPv4 loopback", "127.0.0.1", Loopback},
		{"IPv6 loopback", "::1", Loopback},
		{"RFC1918 ten", "10.0.0.5", Private},
		{"RFC1918 172", "172.16.3.4", Private},
		{"RFC1918 192", "192.168.1.1", Private},
		{"docker bridge is private", "172.17.0.1", Private},
		{"IPv6 ULA", "fd12:3456:789a::1", Private},
		{"carrier-grade NAT", "100.64.0.1", CGNAT},
		{"cloud metadata", "169.254.169.254", LinkLocal},
		{"IPv6 link-local", "fe80::1", LinkLocal},
		{"unspecified v4", "0.0.0.0", Unspecified},
		{"unspecified v6", "::", Unspecified},
		{"multicast", "239.1.2.3", Multicast},
		{"interface-local multicast", "ff01::1", Multicast},
		{"broadcast", "255.255.255.255", Reserved},
		{"reserved 240/4", "240.0.0.1", Reserved},
		{"benchmarking", "198.18.0.1", Reserved},
		{"IETF assignments", "192.0.0.1", Reserved},
		{"TEST-NET-1", "192.0.2.1", Documentation},
		{"TEST-NET-2", "198.51.100.1", Documentation},
		{"TEST-NET-3", "203.0.113.10", Documentation},
		{"IPv6 documentation prefix is not special", "2001:db8::1", Public},

		// net.IP's predicates normalise this one through To4(), which is why
		// it lands on LinkLocal rather than needing its own encoding case.
		{"IPv4-mapped metadata", "::ffff:169.254.169.254", LinkLocal},

		// The encodings that a guard looking only at the outer address family
		// would wave through. Each of these spells 169.254.169.254.
		{"6to4 metadata", "2002:a9fe:a9fe::1", Translated},
		{"NAT64 metadata", "64:ff9b::a9fe:a9fe", Translated},
		{"IPv4-compatible metadata", "::169.254.169.254", Translated},
		{"6to4 private payload", "2002:c0a8:0101::1", Translated},
		{"Teredo", "2001:0000:4136:e378:8000:63bf:3fff:fdd2", Translated},

		// A translation prefix wrapping a public address is public: the
		// payload decides, not the wrapper.
		{"6to4 public payload", "2002:0808:0808::1", Public},
		{"NAT64 public payload", "64:ff9b::0808:0808", Public},

		// fec0::/10 is the gap that motivated sharing this classifier:
		// net.IP.IsPrivate covers fc00::/7 and stops there.
		{"IPv6 site-local", "fec0::1", SiteLocalIPv6},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("test case has an unparseable address: %s", tc.ip)
			}
			class, reason := Classify(ip)
			if class != tc.class {
				t.Fatalf("Classify(%s) = %v (%q), want %v", tc.ip, class, reason, tc.class)
			}
			if tc.class == Public && reason != "" {
				t.Fatalf("Classify(%s) returned reason %q for a public address", tc.ip, reason)
			}
			if tc.class != Public && reason == "" {
				t.Fatalf("Classify(%s) returned class %v with no reason", tc.ip, tc.class)
			}
		})
	}
}

// A nil or truncated address must not read as Public: callers use IsPublic to
// decide whether to dial, so an unparseable value has to fail closed.
func TestClassifyRejectsMalformedAddresses(t *testing.T) {
	t.Parallel()

	for name, ip := range map[string]net.IP{
		"nil":       nil,
		"empty":     {},
		"truncated": {10, 0, 0},
	} {
		class, _ := Classify(ip)
		if class != Invalid {
			t.Fatalf("Classify(%s) = %v, want Invalid", name, class)
		}
		if IsPublic(ip) {
			t.Fatalf("IsPublic(%s) = true, want false", name)
		}
	}
}

func TestIsPublic(t *testing.T) {
	t.Parallel()

	// Documentation ranges are a distinct class precisely because callers
	// disagree about them; IsPublic is the strict answer.
	nonPublic := []string{
		"127.0.0.1",
		"10.0.0.1",
		"100.64.0.1",
		"169.254.169.254",
		"fec0::1",
		"2002:a9fe:a9fe::1",
		"203.0.113.10",
	}
	for _, raw := range nonPublic {
		if IsPublic(net.ParseIP(raw)) {
			t.Errorf("IsPublic(%s) = true, want false", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if !IsPublic(net.ParseIP(raw)) {
			t.Errorf("IsPublic(%s) = false, want true", raw)
		}
	}
}
