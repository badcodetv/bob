// Package secrets encrypts project secrets for storage: AES-256-GCM under BOB_SECRETS_KEY, each
// value bound to its project and name so a stored value cannot be moved to another row.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Box seals and opens secret values.
type Box struct{ aead cipher.AEAD }

// NewBox takes the key as BOB_SECRETS_KEY holds it: 32 random bytes, base64-encoded
// (`openssl rand -base64 32`).
func NewBox(encoded string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(key) != 32 {
		return nil, errors.New("BOB_SECRETS_KEY must be 32 bytes, base64-encoded (openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func label(project, name string) []byte { return []byte("bob-secret\x00" + project + "\x00" + name) }

// Seal encrypts value for project's secret name.
func (b *Box) Seal(project, name, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, b.aead.Seal(nil, nonce, []byte(value), label(project, name)), nil
}

// Open decrypts a stored value. The error never includes the value.
func (b *Box) Open(project, name string, nonce, ciphertext []byte) (string, error) {
	if len(nonce) != b.aead.NonceSize() {
		return "", fmt.Errorf("secret %s of %s: bad nonce", name, project)
	}
	plain, err := b.aead.Open(nil, nonce, ciphertext, label(project, name))
	if err != nil {
		return "", fmt.Errorf("secret %s of %s cannot be decrypted (was BOB_SECRETS_KEY changed?)", name, project)
	}
	return string(plain), nil
}

var namePattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

// reserved are variables the image or the runtime set, which a secret must not replace.
var reserved = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "SHELL": true, "PWD": true, "PORT": true, "HOSTNAME": true, "TERM": true,
	"CLAUDE_CONFIG_DIR": true, "CODEX_HOME": true, "XDG_DATA_HOME": true, "XDG_CONFIG_HOME": true, "XDG_CACHE_HOME": true,
}

// CheckName says why name cannot be a secret's name, or nil.
func CheckName(name string) error {
	switch {
	case !namePattern.MatchString(name):
		return errors.New("a secret's name is an environment variable name: capital letters, digits and _, starting with a letter")
	case reserved[name], strings.HasPrefix(name, "BOB_"), strings.HasPrefix(name, "GIT_"), strings.HasPrefix(name, "LD_"), strings.HasPrefix(name, "NODE_"):
		return fmt.Errorf("%s is set by Bob or the runtime image and cannot be a secret", name)
	}
	return nil
}
