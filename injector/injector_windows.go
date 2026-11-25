//go:build windows

package injector

import (
	"errors"
	"fmt"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/sudochitswe-v2/pasta/translator"
	"golang.org/x/sys/windows"
)

var (
	modUser32     = windows.NewLazySystemDLL("user32.dll")
	procSendInput = modUser32.NewProc("SendInput")
)

const (
	inputKeyboard    = 1
	keyeventfKeyup   = 0x0002
	keyeventfUnicode = 0x0004
	vkTab            = 0x09
	vkReturn         = 0x0D
	vkShift          = 0x10
	vkControl        = 0x11
	vkSpace          = 0x20
	vkV              = 0x56
)

// vkMap translates abstract translator keys to Win32 virtual-key codes
// for the optional kernel-mode DriverBackend.
var vkMap = map[translator.KeyCode]uint16{
	translator.KeyA: 0x41, translator.KeyB: 0x42, translator.KeyC: 0x43,
	translator.KeyD: 0x44, translator.KeyE: 0x45, translator.KeyF: 0x46,
	translator.KeyG: 0x47, translator.KeyH: 0x48, translator.KeyI: 0x49,
	translator.KeyJ: 0x4A, translator.KeyK: 0x4B, translator.KeyL: 0x4C,
	translator.KeyM: 0x4D, translator.KeyN: 0x4E, translator.KeyO: 0x4F,
	translator.KeyP: 0x50, translator.KeyQ: 0x51, translator.KeyR: 0x52,
	translator.KeyS: 0x53, translator.KeyT: 0x54, translator.KeyU: 0x55,
	translator.KeyV: 0x56, translator.KeyW: 0x57, translator.KeyX: 0x58,
	translator.KeyY: 0x59, translator.KeyZ: 0x5A,
	translator.Key1: 0x31, translator.Key2: 0x32, translator.Key3: 0x33,
	translator.Key4: 0x34, translator.Key5: 0x35, translator.Key6: 0x36,
	translator.Key7: 0x37, translator.Key8: 0x38, translator.Key9: 0x39,
	translator.Key0:     0x30,
	translator.KeyMinus: 0xBD, translator.KeyEqual: 0xBB,
	translator.KeyLeftBrace: 0xDB, translator.KeyRightBrace: 0xDD,
	translator.KeyBackslash: 0xDC, translator.KeySemicolon: 0xBA,
	translator.KeyApostrophe: 0xDE, translator.KeyGrave: 0xC0,
	translator.KeyComma: 0xBC, translator.KeyDot: 0xBE, translator.KeySlash: 0xBF,
	translator.KeySpace: vkSpace, translator.KeyEnter: vkReturn,
	translator.KeyTab: vkTab, translator.KeyShift: vkShift,
}

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

func appendKeyPair(inputs []input, vk uint16) []input {
	return append(inputs,
		input{typ: inputKeyboard, ki: keyboardInput{wVk: vk}},
		input{typ: inputKeyboard, ki: keyboardInput{wVk: vk, dwFlags: keyeventfKeyup}},
	)
}

// buildTypeInputs converts text into a sequence of Win32 INPUT make/break
// events. Control/formatting whitespace (\r\n, \n, \r, \t, ' ') uses real
// virtual-key presses (VK_RETURN, VK_TAB, VK_SPACE) because Windows controls
// ignore KEYEVENTF_UNICODE for U+000A/U+000D/U+0009, which otherwise flattens
// multi-line text onto a single line. Printable characters use KEYEVENTF_UNICODE
// so output is independent of the host keyboard layout.
func buildTypeInputs(text string) []input {
	runes := []rune(text)
	inputs := make([]input, 0, len(runes)*2)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case '\r':
			if i+1 < len(runes) && runes[i+1] == '\n' {
				i++
			}
			inputs = appendKeyPair(inputs, vkReturn)
		case '\n':
			inputs = appendKeyPair(inputs, vkReturn)
		case '\t':
			inputs = appendKeyPair(inputs, vkTab)
		case ' ':
			inputs = appendKeyPair(inputs, vkSpace)
		default:
			for _, u := range utf16.Encode([]rune{r}) {
				inputs = append(inputs,
					input{typ: inputKeyboard, ki: keyboardInput{wScan: u, dwFlags: keyeventfUnicode}},
					input{typ: inputKeyboard, ki: keyboardInput{wScan: u, dwFlags: keyeventfUnicode | keyeventfKeyup}},
				)
			}
		}
	}
	return inputs
}

// Type implements Injector (Stealth Type mode): each character is emitted as
// paced down/up key events via SendInput (or the registered kernel driver).
// Newlines and tabs emit VK_RETURN / VK_TAB so formatting is preserved across
// all Windows editors and inputs; other runes emit KEYEVENTF_UNICODE events.
func (w *windowsInjector) Type(text string) error {
	if kernelDriver != nil {
		events, err := translator.Translate(text)
		if err != nil {
			return err
		}
		for _, ev := range events {
			vk, ok := vkMap[ev.Key]
			if !ok {
				return errors.New("injector: no VK mapping")
			}
			if err := kernelDriver.SendKey(vk, ev.Down); err != nil {
				return err
			}
			time.Sleep(translator.DefaultKeyDelay)
		}
		return nil
	}

	inputs := buildTypeInputs(text)
	for i := range inputs {
		if err := sendInputs(inputs[i : i+1]); err != nil {
			return err
		}
		time.Sleep(translator.DefaultKeyDelay)
	}
	return nil
}

// Close is a no-op on Windows (SendInput holds no resources).
func (w *windowsInjector) Close() error { return nil }
