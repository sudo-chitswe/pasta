package network

import (
	"net"
	"strings"
	"testing"
)

func TestClassifyInterface(t *testing.T) {
	tests := []struct {
		name     string
		ip       string
		expected InterfaceType
	}{
		// Hotspot by IP
		{"wlan0", "10.42.0.1", TypeHotspot},
		{"Local Area Connection* 1", "192.168.137.1", TypeHotspot},
		// Hotspot by name
		{"ap0", "192.168.1.100", TypeHotspot},
		{"hotspot0", "192.168.1.50", TypeHotspot},

		// USB Tethering by IP
		{"rndis0", "192.168.42.129", TypeUSB},
		{"enx123", "172.20.10.2", TypeUSB},
		// USB Tethering by name
		{"usb0", "10.0.0.5", TypeUSB},

		// Tailscale by IP (CGNAT range 100.64.0.0/10)
		{"ts0", "100.85.20.10", TypeTailscale},
		{"tailscale0", "192.168.1.10", TypeTailscale},
		{"wg0", "10.10.10.1", TypeTailscale},

		// Wi-Fi
		{"wlan0", "192.168.1.100", TypeWiFi},
		{"wlp3s0", "172.26.91.228", TypeWiFi},
		{"Wi-Fi", "192.168.0.15", TypeWiFi},

		// Ethernet
		{"eth0", "192.168.1.200", TypeEthernet},
		{"enp2s0", "10.0.0.2", TypeEthernet},
		{"Ethernet", "192.168.1.5", TypeEthernet},

		// Virtual / VM
		{"vmnet1", "172.16.53.1", TypeVirtual},
		{"docker0", "172.17.0.1", TypeVirtual},
		{"virbr0", "192.168.122.1", TypeVirtual},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		got := classifyInterface(tt.name, ip)
		if got != tt.expected {
			t.Errorf("classifyInterface(%q, %q) = %v; want %v", tt.name, tt.ip, got, tt.expected)
		}
	}
}

func TestFindInterface(t *testing.T) {
	ifaces := []InterfaceInfo{
		{Name: "wlp3s0", IP: net.ParseIP("172.26.91.228"), Type: TypeWiFi},
		{Name: "ap0", IP: net.ParseIP("10.42.0.1"), Type: TypeHotspot},
		{Name: "tailscale0", IP: net.ParseIP("100.85.12.34"), Type: TypeTailscale},
	}

	// Find by exact IP
	info, err := FindInterface("10.42.0.1", ifaces)
	if err != nil || info.Name != "ap0" {
		t.Fatalf("FindInterface(10.42.0.1) error: %v, info: %+v", err, info)
	}

	// Find by exact name
	info, err = FindInterface("wlp3s0", ifaces)
	if err != nil || info.IP.String() != "172.26.91.228" {
		t.Fatalf("FindInterface(wlp3s0) error: %v, info: %+v", err, info)
	}

	// Find by partial name
	info, err = FindInterface("tail", ifaces)
	if err != nil || info.Name != "tailscale0" {
		t.Fatalf("FindInterface(tail) error: %v, info: %+v", err, info)
	}

	// Not found
	_, err = FindInterface("nonexistent", ifaces)
	if err == nil {
		t.Fatal("expected error for nonexistent interface, got nil")
	}
}

func TestFormatMagicLink(t *testing.T) {
	token := "abc123token"

	tests := []struct {
		name        string
		rawHost     string
		defaultPort int
		useTLS      bool
		wantPrefix  string
		wantHost    string
	}{
		{
			name:        "bare IPv4",
			rawHost:     "172.26.91.228",
			defaultPort: 8765,
			useTLS:      false,
			wantPrefix:  "http://172.26.91.228:8765/?token=" + token,
		},
		{
			name:        "bare IPv4 with TLS",
			rawHost:     "172.26.91.228",
			defaultPort: 8765,
			useTLS:      true,
			wantPrefix:  "https://172.26.91.228:8765/?token=" + token,
		},
		{
			name:        "host with port",
			rawHost:     "10.42.0.1:9000",
			defaultPort: 8765,
			useTLS:      false,
			wantPrefix:  "http://10.42.0.1:9000/?token=" + token,
		},
		{
			name:        "mDNS hostname",
			rawHost:     "thinkpad.local",
			defaultPort: 8765,
			useTLS:      false,
			wantPrefix:  "http://thinkpad.local:8765/?token=" + token,
		},
		{
			name:        "full HTTPS tunnel URL",
			rawHost:     "https://pasta-tunnel.trycloudflare.com",
			defaultPort: 8765,
			useTLS:      false,
			wantPrefix:  "https://pasta-tunnel.trycloudflare.com/?token=" + token,
		},
		{
			name:        "full HTTP URL with custom port",
			rawHost:     "http://localhost:8765",
			defaultPort: 8765,
			useTLS:      false,
			wantPrefix:  "http://localhost:8765/?token=" + token,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatMagicLink(tt.rawHost, tt.defaultPort, token, tt.useTLS)
			if got != tt.wantPrefix {
				t.Errorf("FormatMagicLink(%q, %d, ...) = %q; want %q", tt.rawHost, tt.defaultPort, got, tt.wantPrefix)
			}
		})
	}
}

func TestDetectInterfaces(t *testing.T) {
	ifaces, err := DetectInterfaces()
	if err != nil {
		t.Fatalf("DetectInterfaces() err: %v", err)
	}

	// Should detect at least one interface on a live machine
	if len(ifaces) == 0 {
		t.Log("Note: No active IPv4 interfaces found (container/offline environment)")
		return
	}

	primaryCount := 0
	for _, iface := range ifaces {
		if iface.IsPrimary {
			primaryCount++
		}
		if iface.IP == nil || iface.IP.IsLoopback() {
			t.Errorf("Interface %s returned loopback or nil IP: %v", iface.Name, iface.IP)
		}
		if iface.Description == "" {
			t.Errorf("Interface %s has empty description", iface.Name)
		}
	}

	if primaryCount != 1 {
		t.Errorf("Expected exactly 1 primary interface, got %d", primaryCount)
	}
}

func TestMDNSHost(t *testing.T) {
	h := MDNSHost()
	if h != "" && !strings.Contains(h, ".") {
		t.Errorf("Expected MDNSHost to contain dot, got %q", h)
	}
}
