package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A password written into a URL is masked where the URL is printed, and the
// rest is left as written: re-serialising would change the scheme's case or
// the escaping, and two spellings of one console would then print alike.
func TestRedactURL(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"https://bob:hunter2@console.example.com/p", "https://bob:xxxxx@console.example.com/p"},
		{"HTTPS://bob:hunter2@Console.Example.com/a b", "HTTPS://bob:xxxxx@Console.Example.com/a b"},
		{"http://bob:hunter2@127.0.0.1:8000/x?y=1", "http://bob:xxxxx@127.0.0.1:8000/x?y=1"},
		{"https://bob@console.example.com", "https://bob@console.example.com"},
		{"https://console.example.com/a@b", "https://console.example.com/a@b"},
		{"https://console.example.com?q=a@b", "https://console.example.com?q=a@b"},
		{"https://console.example.com", "https://console.example.com"},
		{"not a url", "not a url"},
	}
	for _, c := range cases {
		if got := RedactURL(c.raw); got != c.want {
			t.Errorf("RedactURL(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// A URL offered as something to run carries no userinfo at all.
func TestWithoutUserinfo(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"HTTPS://bob:hunter2@Console.Example.com/p", "HTTPS://Console.Example.com/p"},
		{"https://bob@console.example.com", "https://console.example.com"},
		{"https://console.example.com/a@b", "https://console.example.com/a@b"},
		{"https://console.example.com", "https://console.example.com"},
	}
	for _, c := range cases {
		if got := WithoutUserinfo(c.raw); got != c.want {
			t.Errorf("WithoutUserinfo(%q) = %q, want %q", c.raw, got, c.want)
		}
	}
}

// A request that fails, or a reply that is not JSON, names the URL it went to
// without the password written into it: these errors reach CI logs. Port 1
// refuses at once, and the stub is on loopback, so nothing leaves the machine.
func TestRequestErrorsMaskAURLPassword(t *testing.T) {
	notJSON := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>not json</html>"))
	}))
	defer notJSON.Close()
	withPassword := func(u string) string { return strings.Replace(u, "http://", "http://bob:hunter2@", 1) }

	fp := filepath.Join(t.TempDir(), "locations.csv")
	if err := os.WriteFile(fp, []byte("latitude,longitude\n1,2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	calls := map[string]func() error{
		"unreachable": func() error {
			return New(withPassword("http://127.0.0.1:1"), "tok").Get(ctx, "/audiences", nil, nil)
		},
		"not JSON": func() error {
			return New(withPassword(notJSON.URL), "tok").Get(ctx, "/audiences", nil, nil)
		},
		"multipart, unreachable": func() error {
			c := New(withPassword("http://127.0.0.1:1"), "tok")
			_, err := CreateMultipart[map[string]any](ctx, c, "/pois", nil, "file", fp)
			return err
		},
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()
			if err == nil {
				t.Fatal("want an error")
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Errorf("the error repeats the URL's password: %v", err)
			}
			if !strings.Contains(err.Error(), "bob:xxxxx@") {
				t.Errorf("err = %v, want it to name the masked URL", err)
			}
		})
	}
}
