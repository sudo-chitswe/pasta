// Command pasta is a stealthy background daemon for remote text paste via
// a local web interface.
//
// It serves an embedded mobile-first web UI, writes received text to the
// system clipboard through native OS APIs (zero subprocesses), and
// simulates Ctrl+V in the active window.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/sudochitswe-v2/pasta/clipboard"
	"github.com/sudochitswe-v2/pasta/injector"
	"github.com/sudochitswe-v2/pasta/network"
	"github.com/sudochitswe-v2/pasta/screenshot"
	"github.com/sudochitswe-v2/pasta/server"
)

//go:embed web/index.html web/style.css web/app.js
var webFS embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pasta:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		port         = flag.Int("port", 8765, "TCP port to listen on")
		bind         = flag.String("bind", "0.0.0.0", "address to bind (use 127.0.0.1 to restrict to this host)")
		verbose      = flag.Bool("verbose", false, "enable request logging (no log files are written)")
		noInject     = flag.Bool("no-inject", false, "clipboard-only mode: skip Ctrl+V keystroke injection")
		maxBytes     = flag.Int("max-bytes", server.DefaultMaxTextBytes, "max paste payload in bytes")
		maxTypeBytes = flag.Int("max-type-bytes", server.DefaultMaxTypeBytes, "max stealth-type payload in bytes")
		tlsMode      = flag.Bool("tls", false, "enable HTTPS with auto-generated self-signed certificate (required for Clipboard API on iOS Safari)")
		qr           = flag.Bool("qr", false, "print a Magic Link QR for this LAN and exit (no daemon)")
		setup        = flag.Bool("setup", false, "alias of --qr")
		hostFlag     = flag.String("host", "", "override host/IP/domain in Magic Link and QR (e.g. 10.42.0.1, 100.85.12.34, thinkpad.local, https://tunnel.example.com)")
		ifaceFlag    = flag.String("interface", "", "network interface name or IP to use for --qr (e.g. wlan0, eth0, 10.42.0.1)")
		listIfaces   = flag.Bool("interfaces", false, "list all active network interfaces and exit")
		allQR        = flag.Bool("all-qr", false, "print QR codes for all detected network interfaces")
		usbMode      = flag.Bool("usb", false, "print Magic Link and QR for USB / ADB reverse tethering (localhost)")
		troubleshoot = flag.Bool("troubleshoot", false, "show troubleshooting guide for enterprise, school, and office networks")
		netHelp      = flag.Bool("network-help", false, "alias of --troubleshoot")
	)
	flag.Parse()

	if *troubleshoot || *netHelp {
		printTroubleshootGuide(*port)
		return nil
	}

	if *listIfaces {
		return listInterfaces(*port)
	}

	if *qr || *setup || *usbMode || *allQR || (*ifaceFlag != "" && !*verbose) {
		return runQR(QROptions{
			Port:      *port,
			UseTLS:    *tlsMode,
			Host:      *hostFlag,
			Interface: *ifaceFlag,
			AllQR:     *allQR,
			USB:       *usbMode,
		})
	}

	token, err := loadOrCreateToken()
	if err != nil {
		return err
	}

	logger := log.New(os.Stderr, "pasta: ", log.LstdFlags)
	if !*verbose {
		logger.SetOutput(discardWriter{})
	}
	_ = logger // verbose logger; errors below always print.

	addr := net.JoinHostPort(strings.TrimSpace(*bind), fmt.Sprint(*port))

	cb, err := clipboard.New()
	if err != nil {
		return fmt.Errorf("clipboard init: %w", err)
	}
	fmt.Printf("pasta: clipboard ready\n")

	var inj injector.Injector
	if *noInject {
		fmt.Printf("pasta: injection disabled (--no-inject), clipboard-only mode\n")
	} else {
		inj, err = injector.New()
		if err != nil {
			// Non-fatal: clipboard sync still works, user pastes manually.
			fmt.Printf("pasta: injector unavailable, clipboard-only mode: %v\n", err)
			inj = nil
		} else {
			fmt.Printf("pasta: injector ready\n")
		}
	}
	if inj != nil {
		defer inj.Close()
	}

	capturer, err := screenshot.New()
	if err != nil {
		fmt.Printf("pasta: screenshot capturer unavailable: %v\n", err)
		capturer = nil
	} else {
		fmt.Printf("pasta: screenshot capturer ready\n")
	}

	srv := server.New(cb, inj, server.Options{
		MaxTextBytes: *maxBytes,
		MaxTypeBytes: *maxTypeBytes,
		NoInject:     *noInject,
		Verbose:      *verbose,
		Token:        token,
		Capturer:     capturer,
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", srv.Routes())
	mux.HandleFunc("/", serveWeb)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           server.AuthMiddleware(token, mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful shutdown on SIGTERM/SIGINT so the uinput device is destroyed.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		fmt.Printf("\npasta: shutting down\n")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
	}()

	certFile, keyFile, err := loadOrCreateTLSCert()
	var tlsCfg *tls.Config
	if err == nil {
		if cert, err := tls.LoadX509KeyPair(certFile, keyFile); err == nil {
			tlsCfg = &tls.Config{
				Certificates: []tls.Certificate{cert},
			}
		}
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	var srvLn net.Listener = ln
	if tlsCfg != nil {
		srvLn = &dualListener{Listener: ln, tlsConfig: tlsCfg}
		if *hostFlag != "" {
			fmt.Printf("pasta: listening on 0.0.0.0:%d (HTTP + HTTPS)\n", *port)
			fmt.Printf("pasta: custom host Magic Link: %s\n", network.FormatMagicLink(*hostFlag, *port, token, true))
		} else if *bind == "0.0.0.0" {
			fmt.Printf("pasta: listening on 0.0.0.0:%d (HTTP + HTTPS) — run `pasta --qr` for the Magic Link\n", *port)
		} else {
			fmt.Printf("pasta: listening on http://%s and https://%s\n", addr, addr)
		}
	} else {
		if *hostFlag != "" {
			fmt.Printf("pasta: listening on 0.0.0.0:%d (HTTP)\n", *port)
			fmt.Printf("pasta: custom host Magic Link: %s\n", network.FormatMagicLink(*hostFlag, *port, token, false))
		} else if *bind == "0.0.0.0" {
			fmt.Printf("pasta: listening on 0.0.0.0:%d (HTTP) — run `pasta --qr` for the Magic Link\n", *port)
		} else {
			fmt.Printf("pasta: listening on http://%s\n", addr)
		}
	}

	if *verbose {
		if ifaces, err := network.DetectInterfaces(); err == nil && len(ifaces) > 0 {
			for _, iface := range ifaces {
				fmt.Printf("pasta: network interface %s (%s) => %s\n", iface.Name, iface.Type, iface.IP.String())
			}
		}
	}

	if err := httpSrv.Serve(srvLn); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}

type sniffConn struct {
	net.Conn
	reader io.Reader
}

func (s *sniffConn) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

type dualListener struct {
	net.Listener
	tlsConfig *tls.Config
}

func (l *dualListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	header := make([]byte, 1)
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	n, err := io.ReadFull(c, header)
	_ = c.SetReadDeadline(time.Time{})

	if err != nil {
		c.Close()
		return nil, err
	}

	combined := &sniffConn{
		Conn:   c,
		reader: io.MultiReader(bytes.NewReader(header[:n]), c),
	}

	// 0x16 is the TLS handshake record type (ClientHello)
	if header[0] == 0x16 {
		return tls.Server(combined, l.tlsConfig), nil
	}

	// Plain HTTP connection
	return combined, nil
}

var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "application/javascript; charset=utf-8",
}

// serveWeb serves the embedded UI. Only the three known assets exist;
// everything else maps to index.html (single-page app).
func serveWeb(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	switch name {
	case "", "index.html":
		name = "index.html"
	case "style.css", "app.js":
	default:
		name = "index.html"
	}
	data, err := fs.ReadFile(webFS, "web/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if ct, ok := contentTypes[path.Ext(name)]; ok {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// lanIP resolves the primary network interface IP address.
func lanIP() (string, error) {
	ifaces, err := network.DetectInterfaces()
	if err != nil {
		return "", err
	}
	for _, iface := range ifaces {
		if iface.IsPrimary {
			return iface.IP.String(), nil
		}
	}
	if len(ifaces) > 0 {
		return ifaces[0].IP.String(), nil
	}
	return "", fmt.Errorf("no active network interfaces found")
}

// magicLink builds the authenticated URL for a host.
func magicLink(ip string, port int, token string, useTLS ...bool) string {
	tlsEnabled := len(useTLS) > 0 && useTLS[0]
	return network.FormatMagicLink(ip, port, token, tlsEnabled)
}

// QROptions contains options for generating pairing QR codes and Magic Links.
type QROptions struct {
	Port      int
	UseTLS    bool
	Host      string
	Interface string
	AllQR     bool
	USB       bool
}

// listInterfaces prints all detected active network adapters with their types and URLs.
func listInterfaces(port int) error {
	ifaces, err := network.DetectInterfaces()
	if err != nil {
		return err
	}
	if len(ifaces) == 0 {
		fmt.Println("pasta: no active non-loopback network interfaces found")
		return nil
	}
	fmt.Println("pasta: Detected Active Network Interfaces:")
	fmt.Println("--------------------------------------------------------------------------------")
	for _, iface := range ifaces {
		primaryTag := ""
		if iface.IsPrimary {
			primaryTag = " [DEFAULT/PRIMARY]"
		}
		fmt.Printf("• %-12s %-16s %-18s%s\n", iface.Name, iface.IP.String(), "["+string(iface.Type)+"]", primaryTag)
		fmt.Printf("  URL: http://%s:%d/\n", iface.IP.String(), port)
	}
	if mdns := network.MDNSHost(); mdns != "" {
		fmt.Printf("• %-12s %-16s %-18s\n", "mDNS", mdns, "[Local Hostname]")
		fmt.Printf("  URL: http://%s:%d/\n", mdns, port)
	}
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Run `pasta --qr --interface <name_or_ip>` to generate a QR for a specific interface.")
	return nil
}

// printTroubleshootGuide outputs step-by-step instructions for bypassing AP/Client Isolation.
func printTroubleshootGuide(port int) {
	fmt.Println(`pasta: Enterprise & Large Network Connectivity Guide
===================================================

If your mobile device is on a different subnet (e.g., 172.26.80.x vs 172.26.91.x)
or your school/office Wi-Fi enforces AP/Client Isolation (dropping peer-to-peer packets),
devices cannot reach each other directly over the shared Wi-Fi.

Here are the 4 recommended ways to connect:

1. Wi-Fi Hotspot on Host (Recommended - Zero Extra Software)
   Create a local hotspot on your laptop and connect your phone to it:
   - Linux:
       nmcli device wifi hotspot ssid PastaHotspot password <your-password>
       pasta --qr --interface 10.42.0.1
   - Windows:
       Turn on "Mobile Hotspot" in Settings -> Network & Internet.
       pasta-cli --qr --interface 192.168.137.1

2. USB Cable / ADB Reverse Tethering (100% Offline, Zero Wi-Fi Needed)
   Plug your phone into your laptop via USB:
   - Android (ADB):
       adb reverse tcp:` + fmt.Sprint(port) + ` tcp:` + fmt.Sprint(port) + `
       pasta --qr --usb
       (Then scan the QR or open http://localhost:` + fmt.Sprint(port) + ` on your phone)
   - USB Tethering:
       Turn on "USB Tethering" in your phone's Settings.
       pasta --qr --interface usb

3. Tailscale Mesh Network (Encrypted Peer-to-Peer Everywhere)
   Install Tailscale on your host and phone (free):
   - Both devices join your private tailnet (100.x.y.z).
   - Run: pasta --qr --host <tailscale-ip-or-magicdns>

4. Instant HTTPS Tunnel (Bypasses Any Firewall & DPI)
   Use Cloudflare Quick Tunnel (genuine HTTPS over TLS, immune to DPI blocking):
   - Linux:
       curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o cloudflared && chmod +x cloudflared
       ./cloudflared tunnel --url http://localhost:` + fmt.Sprint(port) + `
       (Copy the printed https://...trycloudflare.com URL and run):
       pasta --qr --host https://<your-tunnel>.trycloudflare.com
   - Windows:
       cloudflared.exe tunnel --url http://localhost:` + fmt.Sprint(port) + `
       pasta-cli --qr --host https://<your-tunnel>.trycloudflare.com

   Note: SSH-based tunnels (like Pinggy) are often killed by school/office DPI firewalls
   with 'Connection reset by peer' because they detect non-TLS traffic on port 443.`)
}

// runQR prints the Magic Link QR code for the desired host/interface and exits.
func runQR(opts QROptions) error {
	token, err := loadOrCreateToken()
	if err != nil {
		return err
	}

	// 1. USB / ADB Reverse Tethering Mode
	if opts.USB {
		httpLink := network.FormatMagicLink("localhost", opts.Port, token, false)
		httpsLink := network.FormatMagicLink("localhost", opts.Port, token, true)
		link := httpLink
		if opts.UseTLS {
			link = httpsLink
		}
		fmt.Printf("pasta: USB / ADB Reverse Tethering Mode\n")
		fmt.Printf("======================================\n")
		fmt.Printf("1. Ensure your Android phone is connected via USB with USB Debugging enabled.\n")
		fmt.Printf("2. Run this command on your host computer:\n")
		fmt.Printf("     adb reverse tcp:%d tcp:%d\n\n", opts.Port, opts.Port)
		fmt.Printf("3. Scan this QR code or open in your phone's browser:\n   %s\n\n", link)
		if !opts.UseTLS {
			fmt.Printf("   (HTTPS link for iOS Safari): %s\n\n", httpsLink)
		}
		qrterminal.GenerateWithConfig(link, qrterminal.Config{
			Level:      qrterminal.M,
			Writer:     os.Stdout,
			HalfBlocks: true,
		})
		return nil
	}

	// 2. Custom Host / Domain / Tunnel URL Override
	if opts.Host != "" {
		httpLink := network.FormatMagicLink(opts.Host, opts.Port, token, false)
		httpsLink := network.FormatMagicLink(opts.Host, opts.Port, token, true)
		link := httpLink
		if opts.UseTLS {
			link = httpsLink
		}
		fmt.Printf("pasta: Custom Host Magic Link (%s):\n%s\n\n", opts.Host, link)
		if !opts.UseTLS && !strings.HasPrefix(opts.Host, "https://") {
			fmt.Printf("pasta: HTTPS Link (for iOS Safari):\n%s\n\n", httpsLink)
		}
		qrterminal.GenerateWithConfig(link, qrterminal.Config{
			Level:      qrterminal.M,
			Writer:     os.Stdout,
			HalfBlocks: true,
		})
		return nil
	}

	ifaces, err := network.DetectInterfaces()
	if err != nil {
		return err
	}
	if len(ifaces) == 0 {
		return fmt.Errorf("no active network interfaces found (check network connection or specify --host)")
	}

	// 3. User requested a specific interface (by name or IP)
	if opts.Interface != "" {
		target, err := network.FindInterface(opts.Interface, ifaces)
		if err != nil {
			return err
		}
		httpLink := network.FormatMagicLink(target.IP.String(), opts.Port, token, false)
		httpsLink := network.FormatMagicLink(target.IP.String(), opts.Port, token, true)
		link := httpLink
		if opts.UseTLS {
			link = httpsLink
		}
		fmt.Printf("pasta: Magic Link for %s (%s - %s):\n%s\n\n", target.Name, target.IP.String(), target.Type, link)
		if !opts.UseTLS {
			fmt.Printf("pasta: HTTPS Link (for iOS Safari):\n%s\n\n", httpsLink)
		}
		qrterminal.GenerateWithConfig(link, qrterminal.Config{
			Level:      qrterminal.M,
			Writer:     os.Stdout,
			HalfBlocks: true,
		})
		return nil
	}

	// 4. Print all interface QR codes sequentially
	if opts.AllQR {
		for i, iface := range ifaces {
			httpLink := network.FormatMagicLink(iface.IP.String(), opts.Port, token, false)
			link := httpLink
			if opts.UseTLS {
				link = network.FormatMagicLink(iface.IP.String(), opts.Port, token, true)
			}
			fmt.Printf("\n[%d/%d] Interface: %s (%s - %s)\n%s\n\n", i+1, len(ifaces), iface.Name, iface.IP.String(), iface.Type, link)
			qrterminal.GenerateWithConfig(link, qrterminal.Config{
				Level:      qrterminal.M,
				Writer:     os.Stdout,
				HalfBlocks: true,
			})
		}
		return nil
	}

	// 5. Default: render QR for primary interface and list alternatives
	var primary *network.InterfaceInfo
	for i := range ifaces {
		if ifaces[i].IsPrimary {
			primary = &ifaces[i]
			break
		}
	}
	if primary == nil {
		primary = &ifaces[0]
	}

	httpLink := network.FormatMagicLink(primary.IP.String(), opts.Port, token, false)
	httpsLink := network.FormatMagicLink(primary.IP.String(), opts.Port, token, true)
	link := httpLink
	if opts.UseTLS {
		link = httpsLink
	}

	if opts.UseTLS {
		fmt.Printf("pasta: HTTPS Magic Link (%s - %s):\n%s\n\n", primary.Name, primary.Type, link)
	} else {
		fmt.Printf("pasta: Magic Link (%s - %s):\n%s\n\n", primary.Name, primary.Type, link)
		fmt.Printf("pasta: HTTPS Link (for iOS Safari 1-tap clipboard):\n%s\n\n", httpsLink)
	}

	qrterminal.GenerateWithConfig(link, qrterminal.Config{
		Level:      qrterminal.M,
		Writer:     os.Stdout,
		HalfBlocks: true,
	})

	var alternates []network.InterfaceInfo
	for _, iface := range ifaces {
		if iface.IP.String() != primary.IP.String() {
			alternates = append(alternates, iface)
		}
	}

	if len(alternates) > 0 {
		fmt.Printf("\nAlternate Network Interfaces:\n")
		for _, alt := range alternates {
			altLink := network.FormatMagicLink(alt.IP.String(), opts.Port, token, false)
			fmt.Printf("  • %-10s %-15s %-16s %s\n", alt.Name, alt.IP.String(), "["+string(alt.Type)+"]", altLink)
		}
		if mdns := network.MDNSHost(); mdns != "" {
			mdnsLink := network.FormatMagicLink(mdns, opts.Port, token, false)
			fmt.Printf("  • %-10s %-15s %-16s %s\n", "mDNS", mdns, "[Local Host]", mdnsLink)
		}
		fmt.Printf("\nTip: If phone cannot connect on isolated Wi-Fi (e.g. school/office network):\n")
		fmt.Printf("  - Wi-Fi Hotspot: run `pasta --qr --interface hotspot` (or pass hotspot IP)\n")
		fmt.Printf("  - USB Cable:     run `pasta --qr --usb` (with `adb reverse tcp:%d tcp:%d`)\n", opts.Port, opts.Port)
		fmt.Printf("  - Custom Host:   run `pasta --qr --host <ip-or-domain>`\n")
		fmt.Printf("  - Troubleshoot:  run `pasta --troubleshoot` for step-by-step solutions\n")
	}

	return nil
}
