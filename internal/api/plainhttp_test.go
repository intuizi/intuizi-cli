package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Plain http to any host but this machine would carry the bearer token, or on
// a mint the password, readable to anyone on the path. Loopback is where a
// local console runs and where every test stub lives.
func TestCheckBaseURL(t *testing.T) {
	cases := []struct {
		base    string
		refused bool
	}{
		{"http://console.example.com", true},
		{"http://10.0.0.5:8000", true},
		{"HTTP://Console.Example.com/prefix", true},
		{"http://localhost.example.com", true},
		{"https://console.example.com", false},
		{"http://localhost:8000", false},
		{"http://LOCALHOST", false},
		{"http://127.0.0.1:1", false},
		{"http://127.0.0.2:8000", false},
		{"http://[::1]:8000", false},
		// The string checked is the string sent: whitespace is not trimmed
		// into a loopback host the request would never reach.
		{"http://127.0.0.1 ", true},
		{"http://localhost　", true},
		{" http://localhost:8000", true},
		// A URL that does not parse is refused rather than waved through.
		{"http://[::1", true},
	}
	for _, c := range cases {
		t.Run(c.base, func(t *testing.T) {
			err := CheckBaseURL(c.base)
			if (err != nil) != c.refused {
				t.Fatalf("CheckBaseURL(%q) = %v, want refused %v", c.base, err, c.refused)
			}
		})
	}
}

// The refusal names the https URL to use instead.
func TestCheckBaseURLNamesTheHTTPSForm(t *testing.T) {
	err := CheckBaseURL("http://console.example.com:8443/prefix")
	if err == nil || !strings.Contains(err.Error(), "use https://console.example.com:8443/prefix") {
		t.Fatalf("err = %v, want it to name the https URL", err)
	}
}

// The refusal goes to stderr and from there into CI logs, so a password
// written into the URL is not repeated in it.
func TestCheckBaseURLRedactsAPassword(t *testing.T) {
	err := CheckBaseURL("http://bob:hunter2@console.example.com")
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("err = %v, want the plain-http refusal", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the refusal repeats the URL's password: %v", err)
	}
}

// Refused before anything is sent, on both send paths: a transport that
// counts requests stands in for the network.
func TestPlainHTTPIsRefusedBeforeSending(t *testing.T) {
	sent := 0
	count := roundTripperFunc(func(*http.Request) (*http.Response, error) {
		sent++
		return nil, errors.New("sent")
	})

	c := New("http://console.example.com", "tok")
	c.HTTP.Transport = count
	if err := c.Get(context.Background(), "/audiences", nil, nil); err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Errorf("Get: err = %v, want the plain-http refusal", err)
	}

	prev := uploadHTTP
	uploadHTTP = &http.Client{Transport: count}
	t.Cleanup(func() { uploadHTTP = prev })
	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte("latitude,longitude\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := CreateMultipart[map[string]any](context.Background(), c, "/pois", nil, "file", fp)
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Errorf("CreateMultipart: err = %v, want the plain-http refusal", err)
	}

	if sent != 0 {
		t.Errorf("%d request(s) went out over plain http", sent)
	}
}

// The mint carries the account password, so it is refused the same way.
// .invalid never resolves: without the refusal this costs a DNS lookup at most,
// never a connection.
func TestMintAPITokenRefusesPlainHTTP(t *testing.T) {
	_, err := MintAPIToken(context.Background(), "http://console.invalid", "a@b.com", "secret")
	if err == nil || !strings.Contains(err.Error(), "plain http") {
		t.Fatalf("err = %v, want the plain-http refusal", err)
	}
}
