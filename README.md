# Pasta

A stealthy background daemon for remote text paste via a local web interface.
Send text from your phone → it lands in the host via **Fast Paste**
(clipboard + `Ctrl+V`) or **Stealth Type** (raw hardware keystrokes that
bypass clipboard-blocking and secure apps).

- **Language:** Go 1.21+ (stdlib `net/http`, `//go:embed` UI, one dep: `golang.org/x/sys`)
- **Platforms:** Linux (X11 via CGO, `/dev/uinput` injector) & Windows 10/11 (`user32.dll` syscalls, optional KMDF driver backend)
- **Constraint:** zero subprocess spawning — all OS interaction through native APIs

## Installation

### One-Line Install (Recommended)

Install Pasta directly from GitHub without cloning or building:

**Linux:**
```sh
curl -fsSL https://raw.githubusercontent.com/sudochitswe-v2/pasta/master/install.sh | bash
```
> Installs the daemon to `~/.local/bin/pasta`, creates and starts a `systemd` user service with your session's active display (`DISPLAY` / `WAYLAND_DISPLAY`), and displays your phone pairing QR code.

**Windows 10/11 (PowerShell):**
```powershell
irm https://raw.githubusercontent.com/sudochitswe-v2/pasta/master/install.ps1 | iex
```
> Installs `pasta.exe` and `pasta-cli.exe` to `%LOCALAPPDATA%\Pasta`, adds it to user `PATH`, registers background auto-start at logon via the registry, launches the background daemon, and displays your pairing QR code.

---

## Build from Source (Alternative)

```sh
git clone https://github.com/sudochitswe-v2/pasta.git
cd pasta
make build-linux        # -> bin/pasta  (stripped)
./bin/pasta --port 8765 # serves UI on http://<lan-ip>:8765
```

Open the printed URL on your phone, type, hit **Send**.

For Windows:
```powershell
make build-windows      # -> bin\pasta.exe & bin\pasta-cli.exe
```

## First run: pairing your phone (`--qr`)

The daemon is locked by a persistent token (no passwords). On first run it
generates a random 32-byte token at `~/.config/pasta/config.json`
(dir `0700`, file `0600`) and reuses it forever after.

```sh
./bin/pasta --qr            # (or --setup) prints a Magic Link + terminal QR, then exits
```

Scan the QR (or open the link) on the same Wi-Fi. The link
`http://<lan-ip>:8765/?token=…` sets an `HttpOnly` session cookie and
redirects to `/`, so the token leaves browser history and later API calls
just work. API clients can also send `Authorization: Bearer <token>`.
Without a valid token every route (UI included) returns `401`.

## Modes

| Mode | Endpoint | How it works | Best for |
|---|---|---|---|
| **Fast Paste** | `POST /api/paste` (`/api/sync` alias) | Clipboard + `Ctrl+V` | Bulk text, instant |
| **Stealth Type** | `POST /api/type` | Per-character raw key events from a kernel virtual keyboard (2ms micro-sleeps) | Passwords, secure apps, anti-cheat, clipboard blockers |

Stealth Type never touches the clipboard and supports printable ASCII
(letters, digits, US punctuation, space/enter/tab). Unsupported characters
fail loudly with `400` naming the character — no silent drops.

## Flags

| Flag | Default | Meaning |
|---|---|---|
| `--port` | `8765` | TCP port |
| `--bind` | `0.0.0.0` | Bind address — use `127.0.0.1` to restrict to this host |
| `--no-inject` | off | Clipboard-only mode (disables `Ctrl+V` and `/api/type` → `503`) |
| `--max-bytes` | `65536` | Max paste payload in bytes |
| `--max-type-bytes` | `4096` | Max stealth-type payload (typing is paced ~2ms/event) |
| `--tls` | off | Enable HTTPS with auto-generated self-signed certificate (required for Clipboard API on iOS Safari) |
| `--qr`, `--setup` | off | Print the Magic Link QR for this LAN and exit (no daemon) |
| `--host` | `""` | Override host/IP/domain in Magic Link and QR (e.g. `10.42.0.1`, `thinkpad.local`, `https://xyz.trycloudflare.com`) |
| `--interface` | `""` | Select specific network interface name or IP for `--qr` (e.g. `wlan0`, `eth0`, `hotspot`, `10.42.0.1`) |
| `--interfaces` | off | List all active network interfaces with IP, type, and URLs, then exit |
| `--all-qr` | off | Print QR codes for all detected network interfaces sequentially |
| `--usb` | off | Print Magic Link and QR code for USB / ADB reverse tethering (`localhost:8765`) |
| `--troubleshoot` | off | Display step-by-step connection guide for enterprise, school, and office networks |
| `--verbose` | off | Request logging (stderr only — **no log files ever**) |

## API

- `GET /` — embedded mobile web UI (mode toggle, `Ctrl+Enter` to send)
- `GET /api/health` → `{"ok":true}` (UI connection indicator)
- `POST /api/paste` with `{"text":"…"}` → `{"success":true,"injected":true}`
  - `400` empty/invalid JSON · `413` over size limit · `429` rate-limited (30 req/min/IP) · `500` clipboard failure
- `POST /api/sync` — backward-compatible alias of `/api/paste`
- `POST /api/type` with `{"text":"…"}` → `{"success":true,"injected":true,"typed":8}`
  - `400` unsupported character · `413` over 4KB · `503` injection disabled · `500` device failure
- `GET /api/screenshot` / `POST /api/screenshot` → raw `image/png` of current host screen
  - `503` graphical session unavailable · `500` capture failure · `429` rate-limited

## Linux Prerequisites (for `/dev/uinput` auto-paste)

To allow the daemon to emit virtual keyboard keystrokes (`Ctrl+V`) without root:

1. **Load the `uinput` kernel module:**
   ```sh
   sudo modprobe uinput
   echo "uinput" | sudo tee /etc/modules-load.d/uinput.conf   # auto-load on boot
   ```

2. **Add your user to the `input` group:**
   ```sh
   sudo usermod -aG input "$USER"
   ```

3. **Ensure udev permissions rule is configured:**
   ```sh
   echo 'KERNEL=="uinput", GROUP="input", MODE="0660"' | sudo tee /etc/udev/rules.d/99-uinput.rules
   sudo udevadm control --reload-rules && sudo udevadm trigger
   ```
   *Note: Log out and log back in (or run `newgrp input`) for group membership to take effect.*

---

## Firewall Setup (Allow Mobile Phone Access)

If your host runs a firewall such as `ufw`, open port `8765` so your mobile device can reach the web UI:

- **Local Wi-Fi subnet only (recommended):**
  ```sh
  sudo ufw allow from 192.168.1.0/24 to any port 8765 proto tcp comment "Pasta local"
  sudo ufw reload
  ```
- **Allow port from any network:**
  ```sh
  sudo ufw allow 8765/tcp comment "Pasta"
  sudo ufw reload
  ```

---

## Connecting on School, Office & Enterprise Networks (Bypassing AP Isolation)

In large environments (university campuses, corporate offices, dorms), mobile devices and laptops are frequently isolated across different subnets/VLANs (e.g. `172.26.80.x` vs `172.26.91.x`) or blocked by **AP / Client Isolation**, which drops direct peer-to-peer Wi-Fi packets.

Pasta provides four built-in methods to connect seamlessly:

### 1. Laptop Wi-Fi Hotspot (Recommended — No Extra Tools)
Create a direct local Wi-Fi hotspot on your laptop and connect your phone to it:
- **Linux:**
  ```sh
  nmcli device wifi hotspot ssid PastaHotspot password MySecretPassword
  ./bin/pasta --qr --interface 10.42.0.1
  ```
- **Windows:** Turn on "Mobile Hotspot" in Settings -> Network & Internet, then run:
  ```powershell
  pasta-cli --qr --interface 192.168.137.1
  ```
Pasta auto-detects hotspot IP addresses (`10.42.0.1` / `192.168.137.1`) and lists them under `[Hotspot / AP]`.

### 2. USB Cable / ADB Reverse Tethering (100% Offline, Zero Wi-Fi Needed)
Connect your phone to your computer with a USB cable:
- **Android with ADB:**
  ```sh
  adb reverse tcp:8765 tcp:8765
  ./bin/pasta --qr --usb
  ```
  Scan the QR code or open `http://localhost:8765` directly on your phone's browser!
- **USB Tethering:** Turn on "USB Tethering" in your phone settings. Pasta automatically discovers the USB network adapter (`192.168.42.x` on Android or `172.20.10.x` on iOS) and generates the QR code.

### 3. Tailscale / Mesh VPN (Encrypted Mesh Everywhere)
If you use Tailscale, both devices are on your private tailnet (`100.x.y.z`). Run:
```sh
./bin/pasta --qr --host <tailscale-ip-or-magicdns>
```

### 4. Instant HTTPS Tunnel (Bypasses Any Firewall & DPI)
When on restrictive campus/office Wi-Fi with Deep Packet Inspection (DPI), SSH-based tunnels (such as Pinggy) often fail with `Connection reset by peer` because the firewall detects SSH protocol headers on port 443. Use **Cloudflare Quick Tunnel** instead, which uses genuine HTTPS/TLS:

- **Cloudflare Tunnel (Free, No Account Needed):**
  - Linux:
    ```sh
    curl -fsSL https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64 -o cloudflared && chmod +x cloudflared
    ./cloudflared tunnel --url http://localhost:8765
    ```
  - Windows:
    Download `cloudflared-windows-amd64.exe` and run:
    ```powershell
    .\cloudflared.exe tunnel --url http://localhost:8765
    ```
  Copy the generated `https://*.trycloudflare.com` URL and run:
  ```sh
  ./bin/pasta --qr --host https://your-tunnel.trycloudflare.com
  ```

- **Pinggy (SSH Relay):**
  If your network does not block SSH over port 443:
  ```sh
  ssh -p 443 -R0:localhost:8765 a.pinggy.io
  ./bin/pasta --qr --host https://your-subdomain.a.pinggy.io
  ```

Run `./bin/pasta --troubleshoot` anytime to view these instructions in your terminal.

---

## Install as a background service

### Linux (systemd user service)

```sh
make build-linux
./deploy/install.sh
```

This installs the binary to `~/.local/bin/pasta`, configures `~/.config/systemd/user/pasta.service` with your session display (`DISPLAY` / `WAYLAND_DISPLAY`), and enables it to run automatically on login.

#### Managing the Service

| Action | Command |
|---|---|
| **Check service status** | `systemctl --user status pasta` |
| **View live logs** | `journalctl --user -u pasta -f` |
| **Stop service** | `systemctl --user stop pasta` |
| **Start / Resume service** | `systemctl --user start pasta` |
| **Restart service** | `systemctl --user restart pasta` |
| **Disable auto-start on login** | `systemctl --user disable pasta` |
| **Stop and disable completely** | `systemctl --user disable --now pasta` |
| **Re-enable and start** | `systemctl --user enable --now pasta` |

#### Uninstall (Linux)

```sh
systemctl --user disable --now pasta
rm -f ~/.config/systemd/user/pasta.service ~/.local/bin/pasta
systemctl --user daemon-reload
```

---

### Windows (run at logon, no console window)

To install Pasta as a silent background process that automatically starts when you log in:

```powershell
powershell -ExecutionPolicy Bypass -File deploy\install.ps1
```

The installer will:
1. Install both `pasta.exe` (the silent background daemon) and `pasta-cli.exe` (the console utility) to `%LocalAppData%\Pasta`.
2. Add the installation directory to your user `PATH` environment variable.
3. Configure `pasta.exe` to start silently on logon via the registry.
4. Immediately launch the background daemon for you.

**Getting your Magic Link (QR Code) on Windows:**
Because `pasta.exe` runs silently as a Windows GUI application, running it from a terminal will discard all output. Instead, open PowerShell anywhere and use the CLI utility:
```powershell
pasta-cli --qr
```

## Security notes

- LAN exposure: `--bind 0.0.0.0` (default) listens on all interfaces, but
  every route now requires the token — anonymous requests get `401`. Still,
  use trusted networks only (traffic is plain HTTP).
- Token storage: `~/.config/pasta/config.json`, `0600`. Anyone with the
  token (or your unlocked phone session) can inject keystrokes.
- Windows UIPI: `SendInput` is blocked into **elevated** (Administrator)
  windows unless pasta also runs elevated. Clipboard sync still succeeds —
  paste manually with `Ctrl+V` in that case.
- Windows LLKHF: Stealth Type without a kernel driver still carries the
  `LLKHF_INJECTED` flag, which some anti-cheat/secure apps filter. For full
  bypass, install the [Interception](https://github.com/oblitum/Interception)
  driver (or a Nefarius/ViGEmBus-style KMDF filter) with its own installer,
  then wire it via `injector.RegisterDriver()` at startup — the
  `DriverBackend` interface (`SendKey(vk, down)`) is the integration point.
  Without it, pasta uses the `SendInput` fallback and says so in logs.
- Pure-Wayland sessions (no `DISPLAY`): run under XWayland; `/dev/uinput`
  requires membership in the `input` group.

## Stealth profile (measured)

- Idle RSS ≈ 12 MB (< 15 MB target), 0:00 CPU when idle (all threads park in
  `accept`/`XNextEvent` — no polling)
- Stripped binary (`-ldflags="-s -w"`), `-H=windowsgui` on Windows (no console)
- No child processes, no log files, graceful `SIGTERM`/`SIGINT` shutdown

## Development

```sh
make test   # unit + integration (X11/uinput tests skip gracefully w/o display)
make vet
make run    # go run . --verbose
make dist   # builds release archives in dist/
```

## Releasing to GitHub

Pasta includes an automated GitHub Actions release workflow ([`.github/workflows/release.yml`](.github/workflows/release.yml)).

To publish a new GitHub Release with precompiled assets:

1. **Tag and push a release version:**
   ```sh
   git tag v1.0.0
   git push origin v1.0.0
   ```
2. GitHub Actions will automatically:
   - Build stripped binaries for Linux (`amd64`) and Windows (`amd64`).
   - Package `pasta-linux-amd64.tar.gz` and `pasta-windows-amd64.zip`.
   - Generate SHA-256 `checksums.txt`.
   - Publish a new GitHub Release with these assets attached.

Once published, the one-line installer scripts will automatically fetch the latest release archives.

## Layout

```
main.go  server/  clipboard/  injector/  translator/  web/  deploy/  Makefile
```
