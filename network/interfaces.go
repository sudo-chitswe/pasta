package network

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// InterfaceType categorizes the physical or virtual role of a network adapter.
type InterfaceType string

const (
	TypeWiFi      InterfaceType = "Wi-Fi"
	TypeHotspot   InterfaceType = "Hotspot / AP"
	TypeUSB       InterfaceType = "USB Tethering"
	TypeTailscale InterfaceType = "Tailscale / VPN"
	TypeEthernet  InterfaceType = "Ethernet"
	TypeVirtual   InterfaceType = "Virtual / VM"
	TypeOther     InterfaceType = "Other"
)

// InterfaceInfo contains details about a discovered network interface and its IPv4 address.
type InterfaceInfo struct {
	Name        string        `json:"name"`
	IP          net.IP        `json:"ip"`
	Type        InterfaceType `json:"type"`
	Description string        `json:"description"`
	IsPrimary   bool          `json:"is_primary"`
}

// DetectInterfaces returns all active non-loopback network interfaces with an IPv4 address,
// sorted by priority (Primary, Hotspot, USB, Tailscale, Wi-Fi, Ethernet, Virtual).
func DetectInterfaces() ([]InterfaceInfo, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("detect network interfaces: %w", err)
	}

	primaryIP := primaryDialIP()

	var results []InterfaceInfo

	for _, iface := range ifaces {
		// Ignore interfaces that are down or loopback
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}

		for _, addr := range addrs {
			ip := extractIPv4(addr)
			if ip == nil || ip.IsLoopback() {
				continue
			}

			ifaceType := classifyInterface(iface.Name, ip)
			isPrimary := primaryIP != nil && ip.Equal(primaryIP)

			results = append(results, InterfaceInfo{
				Name:        iface.Name,
				IP:          ip,
				Type:        ifaceType,
				Description: formatDescription(iface.Name, ip, ifaceType),
				IsPrimary:   isPrimary,
			})
		}
	}

	// If no interface was flagged as primary via UDP dial, choose the best candidate
	if len(results) > 0 {
		hasPrimary := false
		for _, info := range results {
			if info.IsPrimary {
				hasPrimary = true
				break
			}
		}
		if !hasPrimary {
			// Find first non-virtual interface, or fall back to the first one
			bestIdx := 0
			for i, info := range results {
				if info.Type != TypeVirtual {
					bestIdx = i
					break
				}
			}
			results[bestIdx].IsPrimary = true
		}
	}

	// Sort results by priority
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].IsPrimary != results[j].IsPrimary {
			return results[i].IsPrimary
		}
		pI := typePriority(results[i].Type)
		pJ := typePriority(results[j].Type)
		if pI != pJ {
			return pI < pJ
		}
		return results[i].Name < results[j].Name
	})

	return results, nil
}

// primaryDialIP asks the kernel which local address would be used to route outward.
// No packets are sent; this only queries the OS routing table.
func primaryDialIP() net.IP {
	for _, target := range []string{"8.8.8.8:80", "1.1.1.1:80"} {
		conn, err := net.DialTimeout("udp", target, 800*time.Millisecond)
		if err == nil {
			defer conn.Close()
			if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP.To4() != nil {
				return addr.IP.To4()
			}
		}
	}
	return nil
}

// extractIPv4 extracts a valid IPv4 address from a net.Addr.
func extractIPv4(addr net.Addr) net.IP {
	var ip net.IP
	switch v := addr.(type) {
	case *net.IPNet:
		ip = v.IP
	case *net.IPAddr:
		ip = v.IP
	}
	if ip == nil {
		return nil
	}
	return ip.To4()
}

// classifyInterface categorizes an interface based on its name and IPv4 address.
func classifyInterface(name string, ip net.IP) InterfaceType {
	lowerName := strings.ToLower(name)

	// Check IP subnet ranges
	ip4 := ip.To4()
	if ip4 != nil {
		// Linux NetworkManager default hotspot subnet: 10.42.0.1
		// Windows default Mobile Hotspot (ICS) subnet: 192.168.137.1
		if ip.Equal(net.ParseIP("10.42.0.1")) || ip.Equal(net.ParseIP("192.168.137.1")) {
			return TypeHotspot
		}

		// Android default USB tethering subnet: 192.168.42.0/24
		if ip4[0] == 192 && ip4[1] == 168 && ip4[2] == 42 {
			return TypeUSB
		}

		// iOS default Personal Hotspot / USB tethering subnet: 172.20.10.0/24
		if ip4[0] == 172 && ip4[1] == 20 && ip4[2] == 10 {
			return TypeUSB
		}

		// Tailscale CGNAT IP range: 100.64.0.0/10 (100.64.0.0 to 100.127.255.255)
		if ip4[0] == 100 && (ip4[1] >= 64 && ip4[1] <= 127) {
			return TypeTailscale
		}
	}

	// Check interface name patterns
	switch {
	case containsAny(lowerName, "hotspot", "ap", "hostednetwork", "wlan-ap"):
		return TypeHotspot
	case containsAny(lowerName, "rndis", "usb", "ncm", "iphone"):
		return TypeUSB
	case containsAny(lowerName, "tailscale", "wg", "wireguard", "tun", "tap"):
		return TypeTailscale
	case containsAny(lowerName, "vmnet", "vbox", "docker", "virbr", "br-", "cni", "veth"):
		return TypeVirtual
	case containsAny(lowerName, "wl", "wifi", "wi-fi", "wlan", "wireless"):
		return TypeWiFi
	case containsAny(lowerName, "eth", "enp", "eno", "ethernet"):
		return TypeEthernet
	default:
		return TypeOther
	}
}

func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func typePriority(t InterfaceType) int {
	switch t {
	case TypeHotspot:
		return 1
	case TypeUSB:
		return 2
	case TypeTailscale:
		return 3
	case TypeWiFi:
		return 4
	case TypeEthernet:
		return 5
	case TypeOther:
		return 6
	case TypeVirtual:
		return 7
	default:
		return 8
	}
}

func formatDescription(name string, ip net.IP, t InterfaceType) string {
	return fmt.Sprintf("%s (%s - %s)", ip.String(), name, t)
}

// FindInterface matches an interface by name (case-insensitive substring) or exact IP string.
func FindInterface(query string, ifaces []InterfaceInfo) (*InterfaceInfo, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("empty interface query")
	}

	// 1. Exact IP match
	for i := range ifaces {
		if ifaces[i].IP.String() == query {
			return &ifaces[i], nil
		}
	}

	// 2. Exact name match (case-insensitive)
	for i := range ifaces {
		if strings.EqualFold(ifaces[i].Name, query) {
			return &ifaces[i], nil
		}
	}

	// 3. Substring name match
	for i := range ifaces {
		if strings.Contains(strings.ToLower(ifaces[i].Name), strings.ToLower(query)) {
			return &ifaces[i], nil
		}
	}

	return nil, fmt.Errorf("no active network interface matches %q", query)
}

// MDNSHost returns the local mDNS hostname (e.g. "thinkpad.local") if available.
func MDNSHost() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return ""
	}
	if !strings.Contains(host, ".") {
		return host + ".local"
	}
	return host
}

// FormatMagicLink constructs an authenticated Magic Link URL.
// It handles raw IPs, hostnames, ports, and fully-qualified tunnel URLs (e.g., https://xyz.trycloudflare.com).
func FormatMagicLink(rawHost string, defaultPort int, token string, useTLS bool) string {
	rawHost = strings.TrimSpace(rawHost)
	if rawHost == "" {
		return ""
	}

	// If the user passed a full URL (such as a Cloudflare Tunnel or reverse proxy URL)
	if strings.HasPrefix(rawHost, "http://") || strings.HasPrefix(rawHost, "https://") {
		u, err := url.Parse(rawHost)
		if err == nil && u.Host != "" {
			q := u.Query()
			q.Set("token", token)
			u.RawQuery = q.Encode()
			if u.Path == "" {
				u.Path = "/"
			}
			return u.String()
		}
	}

	scheme := "http"
	if useTLS {
		scheme = "https"
	}

	// Check if rawHost already specifies a port
	if _, _, err := net.SplitHostPort(rawHost); err == nil {
		return fmt.Sprintf("%s://%s/?token=%s", scheme, rawHost, token)
	}

	// Append default port
	formattedAddr := net.JoinHostPort(rawHost, fmt.Sprint(defaultPort))
	return fmt.Sprintf("%s://%s/?token=%s", scheme, formattedAddr, token)
}
