package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	gokeyring "github.com/zalando/go-keyring"
)

// macOS Keychain, Windows Credential Manager, Linux secret service.
// Best-effort: containers and CI have none, so failures fall back to the file.
type keyringStore interface {
	Get(service, key string) (string, error)
	Set(service, key, secret string) error
	Delete(service, key string) error
}

var keyring keyringStore = osKeyring{}

// False when the store is switched off: neither read nor write touches it.
// Unset, empty, or a value that reads as false (0, false) leaves it on, so
// INTUIZI_NO_KEYRING=0 does not mean the opposite of what it says; any other
// value switches it off.
func keyringEnabled() bool {
	v := strings.TrimSpace(os.Getenv(EnvNoKeyring))
	if v == "" {
		return true
	}
	off, err := strconv.ParseBool(v)
	return err == nil && !off
}

// EnvNoKeyring keeps the token in the file: for anyone who prefers that, and
// for tests in other packages, which must not reach the real store.
const EnvNoKeyring = "INTUIZI_NO_KEYRING"

const (
	keyringService = "intuizi-cli"

	// The library sets none; a locked keychain would hang every command.
	keyringTimeout = 2 * time.Second
)

var (
	errKeyringTimeout = errors.New("keyring did not answer in time")
	errNoStore        = errors.New("no credential store")
)

// Keyed by the console the token was minted for: two consoles, two entries.
func keyringKey(baseURL string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/")
}

type osKeyring struct{}

func (osKeyring) Get(service, key string) (string, error) {
	guardTests()
	return withDeadline(func() (string, error) { return gokeyring.Get(service, key) })
}

func (osKeyring) Set(service, key, secret string) error {
	guardTests()
	_, err := withDeadline(func() (string, error) { return "", gokeyring.Set(service, key, secret) })
	return err
}

func (osKeyring) Delete(service, key string) error {
	guardTests()
	_, err := withDeadline(func() (string, error) { return "", gokeyring.Delete(service, key) })
	return err
}

// A test reaching the real store would prompt on macOS or write into CI's.
func guardTests() {
	if testing.Testing() {
		panic("keyring: real store reached in a test - the isolate helper should have replaced it")
	}
}

// Buffered, so the goroutine still exits after a timeout.
func withDeadline(fn func() (string, error)) (string, error) {
	type result struct {
		value string
		err   error
	}
	ch := make(chan result, 1)
	go func() { v, err := fn(); ch <- result{v, err} }()

	select {
	case r := <-ch:
		return r.value, r.err
	case <-time.After(keyringTimeout):
		return "", errKeyringTimeout
	}
}
