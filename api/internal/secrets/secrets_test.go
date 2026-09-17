package secrets

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBox(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	b, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	nonce, ct, err := b.Seal("wolf", "FRED_API_KEY", "s3cret-value")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ct), "s3cret") {
		t.Fatal("ciphertext holds the value")
	}
	if got, err := b.Open("wolf", "FRED_API_KEY", nonce, ct); err != nil || got != "s3cret-value" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := b.Open("enc", "FRED_API_KEY", nonce, ct); err == nil {
		t.Error("a value opened under another project")
	}
	if _, err := b.Open("wolf", "OTHER", nonce, ct); err == nil {
		t.Error("a value opened under another name")
	}
	other, _ := NewBox(base64.StdEncoding.EncodeToString([]byte("fedcba9876543210fedcba9876543210")))
	if _, err := other.Open("wolf", "FRED_API_KEY", nonce, ct); err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("another key: %v", err)
	}
	n2, ct2, _ := b.Seal("wolf", "FRED_API_KEY", "s3cret-value")
	if string(n2) == string(nonce) || string(ct2) == string(ct) {
		t.Error("sealing twice gave the same nonce or ciphertext")
	}
	for _, bad := range []string{"", "short", base64.StdEncoding.EncodeToString([]byte("sixteen bytes!!!")), "not base64 at all!!"} {
		if _, err := NewBox(bad); err == nil {
			t.Errorf("NewBox(%q) accepted", bad)
		}
	}
}

func TestCheckName(t *testing.T) {
	for _, ok := range []string{"FRED_API_KEY", "GITHUB_TOKEN", "ANTHROPIC_API_KEY", "X", "A1_B2"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "fred", "1ABC", "A-B", "A B", "PATH", "HOME", "CLAUDE_CONFIG_DIR", "BOB_USER_EMAIL", "GIT_AUTHOR_EMAIL", "LD_PRELOAD", "NODE_OPTIONS", strings.Repeat("A", 65)} {
		if err := CheckName(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
