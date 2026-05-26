package ui

import "testing"

func TestIconsUnicode(t *testing.T) {
	set := NewIcons(false)
	if set.OK != "✓" || set.Fail != "✗" || set.Run != "⚡" {
		t.Fatalf("unicode set: %+v", set)
	}
}

func TestIconsAsciiFallback(t *testing.T) {
	set := NewIcons(true)
	if set.OK != "[+]" || set.Fail != "[-]" || set.Run != "[*]" {
		t.Fatalf("ascii set: %+v", set)
	}
}
