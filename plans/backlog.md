# Pasta Product Backlog

## Completed Features

### 1. Enterprise & Large Network Connectivity (Completed)
- **Problem Solved:** When mobile devices and the host laptop are placed on different subnets/VLANs (e.g. `172.26.80.x` vs `172.26.91.x`) or on enterprise/university Wi-Fi networks enforcing AP/Client Isolation, peer-to-peer traffic is dropped by network access points.
- **Implemented Capabilities:**
  - **Network Interface Auto-Detection:** Automatically enumerates and classifies active network adapters into Wi-Fi, Hotspot/AP (`10.42.0.1`, `192.168.137.1`), USB Tethering (`192.168.42.x`, `172.20.10.x`), Tailscale/VPN (`100.64.0.0/10`), Ethernet, and mDNS hostname (`<host>.local`).
  - **`--host <ip_or_domain>` Override:** Generates Magic Link & QR code with custom IPs, local domains, or public HTTPS tunnels (`https://*.trycloudflare.com`, `https://*.pinggy.io`).
  - **`--interface <name_or_ip>`:** Directly targets a specific adapter (e.g. `pasta --qr --interface hotspot`).
  - **`--interfaces`:** Lists all active interfaces and their status/URLs.
  - **`--all-qr`:** Sequentially outputs QR codes for all detected networks.
  - **`--usb` Mode:** One-command setup for ADB reverse tethering (`adb reverse tcp:8765 tcp:8765` + `http://localhost:8765/?token=...`).
  - **`--troubleshoot`:** Built-in actionable guide detailing Hotspot, USB, Tailscale, and SSH tunnel workarounds.
  - **Reverse Proxy / Tunnel Support:** Added `X-Forwarded-For` and `X-Real-IP` parsing in the rate limiter and logger for seamless public tunnel routing.

---

## Future Backlog Ideas

### Direct WebRTC DataChannel Pairing (Peer-to-Peer without Port Forwarding)
- Explore lightweight WebRTC DataChannel signaling via a public rendezvous relay to allow direct, encrypted browser-to-host communication through symmetric enterprise NATs without requiring local IP reachability.
