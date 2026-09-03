package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/intuizi/intuizi-cli/internal/output"
)

func quiet(t *testing.T) {
	t.Helper()
	prev := quietOutput
	quietOutput = true
	t.Cleanup(func() { quietOutput = prev })
}

func TestQuietAndJSONContradict(t *testing.T) {
	quiet(t)
	prev := jsonOutput
	jsonOutput = true
	t.Cleanup(func() { jsonOutput = prev })

	err := outputFlagsConflict()
	if err == nil || !strings.Contains(err.Error(), "contradict") {
		t.Fatalf("err = %v", err)
	}
	if got := exitCode(err, nil, true); got != 2 {
		t.Errorf("exit = %d, want 2", got)
	}
	jsonOutput = false
	if err := outputFlagsConflict(); err != nil {
		t.Errorf("--quiet alone should be fine: %v", err)
	}
}

func TestOutputIDReadsIDOrValue(t *testing.T) {
	rec := func(s string) output.Record {
		var r output.Record
		if err := json.Unmarshal([]byte(s), &r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if id, ok := output.ID(rec(`{"id":42,"name":"x"}`)); !ok || id != "42" {
		t.Errorf("id shape: %q %v", id, ok)
	}
	if id, ok := output.ID(rec(`{"value":65,"text":"x"}`)); !ok || id != "65" {
		t.Errorf("value shape: %q %v", id, ok)
	}
	if _, ok := output.ID(rec(`{"name":"x"}`)); ok {
		t.Error("no id should report !ok")
	}
}

func TestQuietCreatePrintsOnlyTheID(t *testing.T) {
	quiet(t)
	srv, _ := stub(t, `{"status":"success","code":201,"data":[{"id":88,"name":"a","status":"Initiating"}]}`)
	path := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(path, []byte(`{"name":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, audiencesCreateCommand(), srv, "--file", path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != "88\n" {
		t.Errorf("stdout = %q, want just the id", out)
	}
}

// POI creates return {value,text}; idOf would find nothing.
func TestQuietPOICreatePrintsTheValue(t *testing.T) {
	quiet(t)
	srv, _ := stub(t, submissionEnvelope)
	out, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "x", "--brand-id", "9", "--file", csvFile(t))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if out != "31\n" {
		t.Errorf("stdout = %q, want the value field", out)
	}
}

func TestQuietListPrintsOneIDPerLine(t *testing.T) {
	quiet(t)
	srv, _ := stub(t, `{"status":"success","code":200,"data":[{"value":1,"text":"a"},{"value":2,"text":"b"},{"value":3,"text":"c"}]}`)
	out, errOut, err := run(t, searchList("brands", "b", poiPrefix+"/brands/index", "none", []string{"value", "text"}), srv)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if out != "1\n2\n3\n" {
		t.Errorf("stdout = %q", out)
	}
	if strings.Contains(errOut, "value") {
		t.Errorf("table leaked to stderr: %q", errOut)
	}
}

func TestQuietShowPrintsTheID(t *testing.T) {
	quiet(t)
	srv, _ := stub(t, `{"status":"success","code":200,"data":[{"id":42,"name":"Shoppers"}]}`)
	out, _, err := run(t, showCommand("audience", audiencesPrefix, nil), srv, "42")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if out != "42\n" {
		t.Errorf("stdout = %q", out)
	}
}

// --wait prints the id whether it succeeded or not; the exit code says which.
func TestQuietWaitPrintsIDOnSuccessAndFailure(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  string
		wantErr bool
	}{
		{"completed", `{"id":104,"name":"Completed"}`, false},
		{"expired", `{"id":106,"name":"Expired"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			quiet(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost {
					_, _ = w.Write([]byte(`{"status":"success","code":201,"data":[{"id":7,"name":"a"}]}`))
					return
				}
				_, _ = w.Write([]byte(`{"status":"success","code":200,"data":[{"id":7,"name":"a","status":` + tc.status + `}]}`))
			}))
			defer srv.Close()
			path := filepath.Join(t.TempDir(), "a.json")
			if err := os.WriteFile(path, []byte(`{"name":"a"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			out, _, err := run(t, audiencesCreateCommand(), srv, "--file", path, "--wait")
			if out != "7\n" {
				t.Errorf("stdout = %q, want the id regardless of outcome", out)
			}
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, errWaitFailed)) {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
