package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func audience(sid int, name string) reply {
	return reply{body: fmt.Sprintf(`{"status":"success","code":200,"data":[
	 {"id":1377,"name":"CLI demo - LAX visitors","status":{"id":%d,"name":%q},
	  "results_count":1189,"is_activation_allowed":true}]}`, sid, name)}
}

func TestAudiencesCreateWaitFollowsToCompleted(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t,
		reply{body: `{"status":"success","code":201,"data":[{"id":1377,"name":"CLI demo - LAX visitors","status":{"id":100,"name":"Initiating"},"results_count":0}]}`},
		audience(100, "Initiating"), audience(101, "Processing"), audience(104, "Completed"))

	path := filepath.Join(t.TempDir(), "audience.json")
	if err := os.WriteFile(path, []byte(`{"name":"CLI demo - LAX visitors","datasets":[{"type":"POI"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errb, err := run(t, audiencesCreateCommand(), srv, "--file", path, "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/analyses/audiences/create" || len(got.paths) != 4 {
		t.Errorf("paths = %v", got.paths)
	}
	for _, p := range got.paths[1:] {
		if p != "/api/v2/analyses/audiences/1377" {
			t.Errorf("poll hit %s", p)
		}
	}
	for _, want := range []string{"created audience 1377", "Initiating (100)", "Processing (101)", "Completed (104)"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
	// Only the final state on stdout, with the audience's lead columns.
	if !strings.HasPrefix(out, "id") || !strings.Contains(out, "1189") || strings.Contains(out, "Initiating") {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
}

// A lookalike trains in 108 Modeling, which is not terminal.
func TestAudiencesShowWaitWaitsThroughModeling(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, audience(108, "Modeling"), audience(108, "Modeling"), audience(104, "Completed"))

	_, errb, err := run(t, audiencesShowCommand(), srv, "1377", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 3 || strings.Count(errb, "Modeling") != 1 {
		t.Errorf("paths = %v, stderr = %q", got.paths, errb)
	}
}

func TestAudiencesCreateWithoutWaitIsUnchanged(t *testing.T) {
	srv, got := stubSeq(t, reply{body: created})

	path := filepath.Join(t.TempDir(), "audience.json")
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, errb, err := run(t, audiencesCreateCommand(), srv, "--file", path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 1 || !strings.Contains(out, "Initiating") || !strings.Contains(errb, "still building") {
		t.Errorf("paths = %v\nstdout = %s\nstderr = %q", got.paths, out, errb)
	}
}

func TestAudiencesTimeoutWithoutWaitIsRejected(t *testing.T) {
	srv, got := stubSeq(t, reply{body: `{}`})
	if _, _, err := run(t, audiencesShowCommand(), srv, "1377", "--timeout", "5m"); err == nil ||
		!strings.Contains(err.Error(), "--timeout only applies with --wait") {
		t.Fatalf("err = %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}
