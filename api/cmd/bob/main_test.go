package main

import "testing"

func TestParseBytes(t *testing.T) {
	for in, want := range map[string]int64{"": 0, "512m": 512 << 20, "8g": 8 << 30, "8G": 8 << 30, "1024": 1024, "4k": 4 << 10} {
		if got, err := parseBytes(in); err != nil || got != want {
			t.Errorf("parseBytes(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"eight", "-1g", "8t"} {
		if _, err := parseBytes(in); err == nil {
			t.Errorf("parseBytes(%q) should fail", in)
		}
	}
}
