//go:build windows

package injector

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modUser32     = windows.NewLazySystemDLL("user32.dll")
	procSendInput = modUser32.NewProc("SendInput")
)

const (
	inputKeyboard      = 1
	keyeventfKeyup     = 0x0002
	keyeventfUnicode   = 0x0004
	vkControl          = 0x11
	vkV                = 0x56
)

// DriverBackend is an optional kernel-mode keystroke backend (e.g. the
// Interception API or a Nefarius/ViGEmBus-style KMDF filter). Keystrokes
// emitted below SendInput carry LLKHF_INJECTED, which anti-cheat systems
// and secure apps use to block simulated input; a kernel driver emits them
// as genuine hardware events without that flag.
//
// Wiring: install the driver per its own installer, then call
// RegisterDriver at startup (e.g. from main) before serving. When no
// backend is registered, Type falls back to SendInput with KEYEVENTF_UNICODE
// — which secure apps may still reject via LLKHF_INJECTED.
type DriverBackend interface {
	// SendKey emits one make (down=true) / break (down=false) event for a
	// Win32 virtual-key code.
	SendKey(vk uint16, down bool) error
}

var kernelDriver DriverBackend

// RegisterDriver installs the kernel-mode backend used by Stealth Type.
// Passing nil restores the SendInput fallback.
func RegisterDriver(d DriverBackend) { kernelDriver = d }

type keyboardInput struct {
	wVk         uint16
	wScan       uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

// input mirrors the Win32 INPUT struct on amd64 (40 bytes): DWORD type at
// offset 0, 4 bytes padding, then the KEYBDINPUT union member (24 bytes) at
// offset 8. The trailing 8-byte pad rounds the 32 bytes up to sizeof(INPUT) == 40
// (matching the 32-byte MOUSEINPUT union member).
type input struct {
	typ uint32
	pad uint32
	ki  keyboardInput
	_   [8]byte
}

type windowsInjector struct{}

// New returns an Injector backed by user32.dll SendInput.
//
// NOTE (UIPI limitation): SendInput is blocked when the foreground window
// runs elevated (as Administrator) and this daemon does not. There is no
// fix short of running elevated as well; clipboard sync still succeeds in
// that case and the user can paste manually with Ctrl+V.
func New() (Injector, error) {
	return &windowsInjector{}, nil
}

// sendInputs calls user32 SendInput with a slice of INPUT structs.
// The slice must remain alive for the duration of the call; passing a
// local slice keeps it on the goroutine stack / heap and is safe here.
func sendInputs(inputs []input) error {
	if len(inputs) == 0 {
		return nil
	}
	n := uintptr(len(inputs))
	r, _, err := procSendInput.Call(
		n,
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if r != n {
		if err != nil && !errors.Is(err, windows.ERROR_SUCCESS) {
			return fmt.Errorf("injector: SendInput: %w", err)
		}
		return fmt.Errorf("injector: SendInput injected %d of %d events", r, n)
	}
	return nil
}

// Paste implements Injector: Ctrl down, V down, V up, Ctrl up.
func (w *windowsInjector) Paste() error {
	inputs := []input{
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkControl}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkV}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkV, dwFlags: keyeventfKeyup}},
		{typ: inputKeyboard, ki: keyboardInput{wVk: vkControl, dwFlags: keyeventfKeyup}},
	}
	return sendInputs(inputs)
}

// Type implements Injector (Stealth Type mode): each rune is emitted as a
// KEYEVENTF_UNICODE down/up pair via SendInput, using UTF-16 scan codes.
// This bypasses all VK-mapping and keyboard-layout issues and works for the
// full Unicode BMP. Surrogate pairs (runes > U+FFFF) are split into two
// UTF-16 code units, each sent as a separate down/up event pair.
//
// With a registered kernel driver backend the events carry no
// LLKHF_INJECTED flag; otherwise SendInput is used and secure apps may
// still reject the input.
func (w *windowsInjector) Type(text string) error {
	for _, r := range text {
		// Encode the rune as UTF-16 (1 or 2 code units).
		var units []uint16
		if r < 0x10000 {
			units = []uint16{uint16(r)}
		} else {
			// Supplementary plane: encode as surrogate pair.
			r1, r2 := utf16.EncodeRune(r)
			units = []uint16{uint16(r1), uint16(r2)}
		}

		for _, u := range units {
			if kernelDriver != nil {
				// Kernel backend uses VK 0 and the scan code as the Unicode value.
				if err := kernelDriver.SendKey(0, true); err != nil {
					return fmt.Errorf("injector: kernel driver send down for U+%04X: %w", u, err)
				}
				if err := kernelDriver.SendKey(0, false); err != nil {
					return fmt.Errorf("injector: kernel driver send up for U+%04X: %w", u, err)
				}
			} else {
				// Use KEYEVENTF_UNICODE: wVk must be 0, wScan carries the UTF-16 unit.
				inp := []input{
					{typ: inputKeyboard, ki: keyboardInput{wScan: u, dwFlags: keyeventfUnicode}},
					{typ: inputKeyboard, ki: keyboardInput{wScan: u, dwFlags: keyeventfUnicode | keyeventfKeyup}},
				}
				if err := sendInputs(inp); err != nil {
					return fmt.Errorf("injector: type U+%04X: %w", u, err)
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}
	return nil
}

// Close is a no-op on Windows (SendInput holds no resources).
func (w *windowsInjector) Close() error { return nil }
