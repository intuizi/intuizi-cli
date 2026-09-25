package config

import "sync"

// Stands in for the OS store: the real one would prompt on macOS and write
// into the CI runner's.
type fakeKeyring struct {
	mu         sync.Mutex
	data       map[string]string
	fail       bool  // simulates a machine with no credential store
	failDelete bool  // simulates a store that reads but will not delete
	getErr     error // Get returns this, for the timeout the fake cannot reach
}

func newFakeKeyring() *fakeKeyring { return &fakeKeyring{data: map[string]string{}} }

func (f *fakeKeyring) Get(service, key string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return "", f.getErr
	}
	if f.fail {
		return "", errNoStore
	}
	v, ok := f.data[service+"\x00"+key]
	if !ok {
		return "", errNoStore
	}
	return v, nil
}

func (f *fakeKeyring) Set(service, key, secret string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errNoStore
	}
	f.data[service+"\x00"+key] = secret
	return nil
}

func (f *fakeKeyring) Delete(service, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail || f.failDelete {
		return errNoStore
	}
	delete(f.data, service+"\x00"+key)
	return nil
}

// Installs the fake for one test, restoring the real one after.
func swapKeyring(t interface{ Cleanup(func()) }) *fakeKeyring {
	f := newFakeKeyring()
	prev := keyring
	keyring = f
	t.Cleanup(func() { keyring = prev })
	return f
}

// Reaches the fake isolate installed, for tests simulating no store.
func fakeKeyringFor(t interface{ Fatal(...any) }) *fakeKeyring {
	f, ok := keyring.(*fakeKeyring)
	if !ok {
		t.Fatal("keyring is not the fake - call isolate first")
	}
	return f
}
