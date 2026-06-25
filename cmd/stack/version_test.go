package main

import "testing"

func TestIsDevVersion(t *testing.T) {
	dev := []string{
		"dev",
		"",
		"0.1.0+dirty",
		"v0.1.1-0.20260625034148-122dbf7c4bc1", // go install of an untagged commit
		"v0.1.1-0.20260625034148-122dbf7c4bc1+dirty", // dirty local build
	}
	for _, v := range dev {
		if !isDevVersion(v) {
			t.Errorf("isDevVersion(%q) = false, want true (dev build)", v)
		}
	}
	release := []string{"0.1.0", "v0.1.0", "1.2.3", "v2.0.0-rc1"}
	for _, v := range release {
		if isDevVersion(v) {
			t.Errorf("isDevVersion(%q) = true, want false (clean release)", v)
		}
	}
}
