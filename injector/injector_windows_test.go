//go:build windows && amd64

package injector

import (
	"testing"
	"unsafe"
)

func TestWindowsInputStructLayout(t *testing.T) {
	if got := unsafe.Sizeof(keyboardInput{}); got != 24 {
		t.Fatalf("sizeof(keyboardInput) = %d, want 24", got)
	}
	if got := unsafe.Offsetof(input{}.ki); got != 8 {
		t.Fatalf("offsetof(input.ki) = %d, want 8", got)
	}
	if got := unsafe.Sizeof(input{}); got != 40 {
		t.Fatalf("sizeof(input) = %d, want 40", got)
	}
}
