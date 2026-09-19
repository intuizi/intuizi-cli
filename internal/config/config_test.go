package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// isolate points the config package at a throwaway directory and clears the
// env token, so tests never touch the developer's real credentials.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("AppData", dir) // os.UserConfigDir() reads this on Windows
	t.Setenv(EnvToken, "")
	return dir
}

func TestPathRespectsXDGConfigHome(t *testing.T) {
	dir := isolate(t)

	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "intuizi", "config.json")
	if got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	isolate(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("first run should not error: %v", err)
	}
	if cfg.Token != "" || cfg.BaseURL != "" || cfg.ExpiresAt != "" {
		t.Fatalf("want zero Config, got %+v", cfg)
	}
}

func TestLoadRejectsMalformedJSON(t *testing.T) {
	isolate(t)

	path, _ := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("malformed JSON should be an error, not a silent empty config")
	}
	// The user has to find and fix the file, so the error must name it.
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("error = %v, want %q and \"not valid JSON\"", err, path)
	}
}

func TestLoadNamesTheFileOnReadError(t *testing.T) {
	isolate(t)

	// A directory where the file should be: ReadFile fails without ErrNotExist.
	path, _ := Path()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	if err == nil {
		t.Fatal("a config path that is a directory should be an error")
	}
	if !strings.Contains(err.Error(), "reading "+path) {
		t.Fatalf("error = %v, want it to start with \"reading %s\"", err, path)
	}
}

// Login needs the directory writable before it spends one of the account's
// ten token slots; EnsureDir is what it checks with.
func TestEnsureDirCreatesOwnerOnlyDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner-only permission bits are a Unix guarantee; Windows uses ACLs")
	}
	isolate(t)

	if err := EnsureDir(); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	fi, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() || fi.Mode().Perm() != 0700 {
		t.Fatalf("config dir = %v, want a 0700 directory", fi.Mode())
	}
	if err := EnsureDir(); err != nil {
		t.Fatalf("second call should be a no-op: %v", err)
	}
}

func TestEnsureDirReportsUnwritableParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod does not restrict directory creation on Windows")
	}
	if os.Getuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := isolate(t)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })

	if err := EnsureDir(); err == nil {
		t.Fatal("expected an error creating a directory under a read-only parent")
	}
}

func TestSaveCreatesFileWithOwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner-only permission bits are a Unix guarantee; Windows uses ACLs")
	}
	isolate(t)

	if err := Save(&Config{BaseURL: "https://example.com", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	path, _ := Path()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("permissions = %v, want 0600", perm)
	}
}

// The file holds a bearer token, so Save must tighten permissions on a file
// that already exists. os.WriteFile's perm argument applies only on create, so
// a plain write would leave a pre-existing 0644 file world-readable.
func TestSaveTightensPermissionsOnExistingFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("owner-only permission bits are a Unix guarantee; Windows uses ACLs")
	}
	isolate(t)

	path, _ := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	if err := Save(&Config{BaseURL: "https://example.com", Token: "secret"}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0600 {
		t.Fatalf("permissions after overwriting a 0644 file = %v, want 0600", perm)
	}
}

func TestSaveLeavesNoTempFiles(t *testing.T) {
	isolate(t)

	if err := Save(&Config{Token: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := Save(&Config{Token: "b"}); err != nil {
		t.Fatal(err)
	}

	path, _ := Path()
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("want only config.json, got %v", names)
	}
}

func TestSaveOmitsEmptyExpiresAt(t *testing.T) {
	isolate(t)

	if err := Save(&Config{BaseURL: "https://example.com", Token: "t"}); err != nil {
		t.Fatal(err)
	}

	path, _ := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "expires_at") {
		t.Fatalf("expires_at should be omitted when empty:\n%s", raw)
	}
}

func TestSaveRoundTrips(t *testing.T) {
	isolate(t)

	want := &Config{
		BaseURL:   "https://staging.example.com",
		Token:     "secret",
		ExpiresAt: "2027-07-29T00:00:00+00:00",
	}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Fatalf("round trip: got %+v, want %+v", got, want)
	}
}

func TestBaseURLPrecedence(t *testing.T) {
	tests := []struct {
		name     string
		stored   string
		override string
		want     string
	}{
		{"nothing set falls back to the default", "", "", DefaultBaseURL},
		{"stored value beats the default", "https://stored.example.com", "", "https://stored.example.com"},
		{"flag beats the stored value", "https://stored.example.com", "https://flag.example.com", "https://flag.example.com"},
		{"trailing slash trimmed from the flag", "", "https://flag.example.com/", "https://flag.example.com"},
		{"trailing slash trimmed from the stored value", "https://stored.example.com/", "", "https://stored.example.com"},
		{"whitespace-only flag is treated as unset", "https://stored.example.com", "   ", "https://stored.example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isolate(t)
			if tc.stored != "" {
				if err := Save(&Config{BaseURL: tc.stored}); err != nil {
					t.Fatal(err)
				}
			}
			if got := BaseURL(tc.override); got != tc.want {
				t.Fatalf("BaseURL(%q) = %q, want %q", tc.override, got, tc.want)
			}
		})
	}
}

func TestTokenSourceNoneWhenNothingStored(t *testing.T) {
	isolate(t)

	tok, src := TokenSource()
	if tok != "" || src != SourceNone {
		t.Fatalf("got (%q, %q), want (\"\", SourceNone)", tok, src)
	}
	if Token() != "" {
		t.Fatalf("Token() = %q, want empty", Token())
	}
}

func TestTokenSourceReadsConfig(t *testing.T) {
	isolate(t)

	if err := Save(&Config{Token: "from-file"}); err != nil {
		t.Fatal(err)
	}

	tok, src := TokenSource()
	if tok != "from-file" || src != SourceConfig {
		t.Fatalf("got (%q, %q), want (\"from-file\", SourceConfig)", tok, src)
	}
}

// CI sets INTUIZI_API_TOKEN and expects it to win over anything on disk.
func TestTokenSourceEnvBeatsConfig(t *testing.T) {
	isolate(t)

	if err := Save(&Config{Token: "from-file"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvToken, "from-ci")

	tok, src := TokenSource()
	if tok != "from-ci" || src != SourceEnv {
		t.Fatalf("got (%q, %q), want (\"from-ci\", SourceEnv)", tok, src)
	}
	if Token() != "from-ci" {
		t.Fatalf("Token() = %q, want from-ci", Token())
	}
}

func TestTokenSourceIgnoresUnreadableConfig(t *testing.T) {
	isolate(t)

	path, _ := Path()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}

	// status should be able to say "not logged in" rather than fail outright.
	if tok, src := TokenSource(); tok != "" || src != SourceNone {
		t.Fatalf("got (%q, %q), want (\"\", SourceNone)", tok, src)
	}
}

func TestClearTokenKeepsBaseURL(t *testing.T) {
	isolate(t)

	if err := Save(&Config{
		BaseURL:   "https://staging.example.com",
		Token:     "secret",
		ExpiresAt: "2027-07-29T00:00:00+00:00",
	}); err != nil {
		t.Fatal(err)
	}

	if err := ClearToken(); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "" {
		t.Fatalf("token not cleared: %q", cfg.Token)
	}
	if cfg.ExpiresAt != "" {
		t.Fatalf("expiry not cleared: %q", cfg.ExpiresAt)
	}
	if cfg.BaseURL != "https://staging.example.com" {
		t.Fatalf("base URL should survive logout, got %q", cfg.BaseURL)
	}
}

// Scripts may call logout more than once; it should not start failing.
func TestClearTokenIsIdempotent(t *testing.T) {
	isolate(t)

	if err := ClearToken(); err != nil {
		t.Fatalf("clearing with no config at all: %v", err)
	}
	if err := Save(&Config{Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearToken(); err != nil {
		t.Fatalf("first clear: %v", err)
	}
	if err := ClearToken(); err != nil {
		t.Fatalf("second clear: %v", err)
	}
}

func TestClearTokenDoesNotContactTheServer(t *testing.T) {
	isolate(t)

	// Logout is deliberately local-only: the revoke endpoint is all-or-nothing
	// and would kill CI's token too. Guard against anyone wiring it in later by
	// checking the file is the only thing that changes.
	if err := Save(&Config{BaseURL: "https://unreachable.invalid", Token: "secret"}); err != nil {
		t.Fatal(err)
	}
	if err := ClearToken(); err != nil {
		t.Fatalf("logout must work without network access: %v", err)
	}
}

func TestStoredFileIsValidJSON(t *testing.T) {
	isolate(t)

	if err := Save(&Config{BaseURL: "https://example.com", Token: "t"}); err != nil {
		t.Fatal(err)
	}

	path, _ := Path()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var into map[string]any
	if err := json.Unmarshal(raw, &into); err != nil {
		t.Fatalf("stored file is not valid JSON: %v\n%s", err, raw)
	}
	if into["base_url"] != "https://example.com" || into["token"] != "t" {
		t.Fatalf("unexpected keys: %v", into)
	}
}

// root.go validates --base-url with this before any request: a bare host
// would otherwise fail deep inside net/http with a message about the scheme.
func TestValidateBaseURL(t *testing.T) {
	ok := []string{
		"https://console.intuizi.com",
		"http://localhost:8000",
		"https://example.com/prefix/",
		"  https://example.com  ",
	}
	for _, raw := range ok {
		if err := ValidateBaseURL(raw); err != nil {
			t.Errorf("ValidateBaseURL(%q) = %v, want nil", raw, err)
		}
	}

	bad := []struct{ raw, want, fix string }{
		{"example.invalid", "has no scheme", "use https://example.invalid"},
		{"example.invalid:8080", "has no scheme", "use https://example.invalid:8080"},
		{"ftp://example.invalid", "ftp", "http or https"},
		{"https://", "has no host", ""},
		{"", "is empty", ""},
		{"https://example.com/?x=1", "query", ""},
		{"https://example.com/#frag", "fragment", ""},
		{"https://exa mple.com", "not a valid URL", ""},
	}
	for _, tc := range bad {
		err := ValidateBaseURL(tc.raw)
		if err == nil {
			t.Errorf("ValidateBaseURL(%q) = nil, want an error", tc.raw)
			continue
		}
		if !strings.Contains(err.Error(), "--base-url") {
			t.Errorf("ValidateBaseURL(%q) = %v, want it to name the flag", tc.raw, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ValidateBaseURL(%q) = %v, want %q", tc.raw, err, tc.want)
		}
		if tc.fix != "" && !strings.Contains(err.Error(), tc.fix) {
			t.Errorf("ValidateBaseURL(%q) = %v, want the fix %q", tc.raw, err, tc.fix)
		}
	}
}
