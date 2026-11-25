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

func TestBuildTypeInputsPreservesFormatting(t *testing.T) {
	got := buildTypeInputs("a\nb\r\n\tc d")

	want := []struct {
		vk    uint16
		scan  uint16
		flags uint32
	}{
		{0, 'a', keyeventfUnicode},
		{0, 'a', keyeventfUnicode | keyeventfKeyup},
		{vkReturn, 0, 0},
		{vkReturn, 0, keyeventfKeyup},
		{0, 'b', keyeventfUnicode},
		{0, 'b', keyeventfUnicode | keyeventfKeyup},
		{vkReturn, 0, 0},
		{vkReturn, 0, keyeventfKeyup},
		{vkTab, 0, 0},
		{vkTab, 0, keyeventfKeyup},
		{0, 'c', keyeventfUnicode},
		{0, 'c', keyeventfUnicode | keyeventfKeyup},
		{vkSpace, 0, 0},
		{vkSpace, 0, keyeventfKeyup},
		{0, 'd', keyeventfUnicode},
		{0, 'd', keyeventfUnicode | keyeventfKeyup},
	}

	if len(got) != len(want) {
		t.Fatalf("len(inputs) = %d, want %d", len(got), len(want))
	}
	for i, w := range want {
		k := got[i].ki
		if k.wVk != w.vk || k.wScan != w.scan || k.dwFlags != w.flags {
			t.Fatalf("input[%d] = (vk=%#x, scan=%#x, flags=%#x), want (vk=%#x, scan=%#x, flags=%#x)",
				i, k.wVk, k.wScan, k.dwFlags, w.vk, w.scan, w.flags)
		}
	}
}
