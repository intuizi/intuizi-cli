package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// reply is one canned response. A zero status means 200.
type reply struct {
	status int
	body   string
}

// stubSeq serves replies[i] on the i-th request and the last reply thereafter,
// so a test can script a status sequence: Initiating, Processing, Completed.
func stubSeq(t *testing.T, replies ...reply) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	var (
		mu sync.Mutex
		n  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		i := n
		n++
		if i >= len(replies) {
			i = len(replies) - 1
		}
		got.paths = append(got.paths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if replies[i].status != 0 {
			w.WriteHeader(replies[i].status)
		}
		_, _ = w.Write([]byte(replies[i].body))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// fast shrinks the poll interval so a test never actually waits.
func fast(t *testing.T) {
	t.Helper()
	prev := pollInterval
	pollInterval = time.Millisecond
	t.Cleanup(func() { pollInterval = prev })
}

// activation is a read of activation 501 at one lifecycle status, in the real
// shape: data is a one-element array and status is an {id, name} object.
func activation(sid int, name string) reply {
	return reply{body: fmt.Sprintf(`{"status":"success","code":200,"data":[
	 {"id":501,"description":"Q1 retail export","status":{"id":%d,"name":%q},
	  "audience":{"id":88,"name":"Coffee Buyers NYC"},"datastreams":[]}]}`, sid, name)}
}

var createdActivation = reply{body: `{"status":"success","code":201,"data":[
 {"id":501,"description":"Q1 retail export","status":{"id":100,"name":"Initiating"}}]}`}

var threeIDs = []string{"--audience-id", "88", "--endpoint-connection-id", "12", "--pricing-model-id", "3"}

const activationPath501 = "/api/v2/analyses/activations/501"

func TestCreateWaitFollowsToCompleted(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, createdActivation,
		activation(100, "Initiating"), activation(101, "Processing"),
		activation(105, "DataStreaming"), activation(104, "Completed"))

	out, errb, err := run(t, activationsCreateCommand(), srv, append(threeIDs, "--wait")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got.paths[0] != "/api/v2/analyses/activations/create" {
		t.Errorf("first call = %s", got.paths[0])
	}
	if len(got.paths) != 5 {
		t.Errorf("expected create + 4 polls, got %d calls: %v", len(got.paths), got.paths)
	}
	for _, p := range got.paths[1:] {
		if p != activationPath501 {
			t.Errorf("poll hit %s", p)
		}
	}

	// Every transition on stderr, once each, in order.
	for _, want := range []string{"created activation 501", "Initiating (100)",
		"Processing (101)", "DataStreaming (105)", "Completed (104)"} {
		if !strings.Contains(errb, want) {
			t.Errorf("stderr missing %q:\n%s", want, errb)
		}
	}
	// Only the final state on stdout.
	if !strings.HasPrefix(out, "id") || !strings.Contains(out, "Completed") {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
	if strings.Contains(out, "Initiating") {
		t.Errorf("the Initiating record leaked onto stdout:\n%s", out)
	}
}

func TestCreateWaitFromFile(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, createdActivation, activation(104, "Completed"))

	path := filepath.Join(t.TempDir(), "activation.json")
	if err := os.WriteFile(path, []byte(`{"audience_id":88,"endpoint_connection_id":12,"pricing_model_id":3,"datastreams":[{"id":7}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, errb, err := run(t, activationsCreateCommand(), srv, "--file", path, "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 2 || !strings.Contains(errb, "created activation 501") {
		t.Errorf("paths = %v, stderr = %q", got.paths, errb)
	}
}

// 105 is numerically above 104 but comes before it; a ">= 104 means done"
// shortcut would exit while files are still moving.
func TestWaitDoesNotStopAtDataStreaming(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, activation(105, "DataStreaming"), activation(105, "DataStreaming"), activation(104, "Completed"))

	_, errb, err := run(t, activationsShowCommand(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 3 {
		t.Errorf("expected 3 polls, got %d", len(got.paths))
	}
	// A repeated status is reported once, not per poll.
	if n := strings.Count(errb, "DataStreaming"); n != 1 {
		t.Errorf("DataStreaming reported %d times:\n%s", n, errb)
	}
}

func TestWaitFailsOnErrorStatusWithDatastreamDetail(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, activation(101, "Processing"), reply{body: `{"status":"success","code":200,"data":[
	 {"id":501,"status":{"id":400,"name":"Error"},
	  "datastreams":[{"name":"Match File","status":"failed","error":"partner rejected credentials"}]}]}`})

	out, _, err := run(t, activationsShowCommand(), srv, "501", "--wait")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	for _, want := range []string{"activation 501 failed", "Error (400)", "Match File: partner rejected credentials"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if out != "" {
		t.Errorf("stdout should stay clean on failure, got %q", out)
	}
}

// An id this CLI has never seen must fail, not wait: waiting is the one outcome
// that can hang a pipeline for the full timeout.
func TestWaitFailsOnUnknownStatus(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, activation(999, "Mystery"))

	_, _, err := run(t, activationsShowCommand(), srv, "501", "--wait")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	if len(got.paths) != 1 {
		t.Errorf("should have stopped after one poll, got %d", len(got.paths))
	}
}

func TestWaitTimesOutWithResumeHint(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, activation(101, "Processing"))

	start := time.Now()
	_, _, err := run(t, activationsShowCommand(), srv, "501", "--wait", "--timeout", "30ms")
	if !errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, want errWaitTimeout", err)
	}
	for _, want := range []string{"activation 501", "last status Processing", "show 501 --wait"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	// A regression to time.Sleep would hold the whole interval past the deadline.
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("timeout took %s; the sleep is not honouring the deadline", elapsed)
	}
}

func TestWaitJSONPrintsOneEnvelope(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, activation(105, "DataStreaming"), activation(104, "Completed"))

	out, _, err := runJSON(t, activationsShowCommand(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// Two polls plus one re-read for the verbatim envelope.
	if len(got.paths) != 3 {
		t.Errorf("expected 3 calls, got %d", len(got.paths))
	}

	dec := json.NewDecoder(strings.NewReader(out))
	var env struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if env.Code != 200 || len(env.Data) == 0 {
		t.Errorf("not the server envelope:\n%s", out)
	}
	if dec.More() {
		t.Errorf("stdout carries more than one JSON document:\n%s", out)
	}
}

func TestTimeoutWithoutWaitIsRejectedOffline(t *testing.T) {
	srv, got := stubSeq(t, reply{body: `{}`})

	for name, tc := range map[string]struct {
		cmd  func() *cobra.Command
		args []string
	}{
		"create": {activationsCreateCommand, append(threeIDs, "--timeout", "5m")},
		"show":   {activationsShowCommand, []string{"501", "--timeout", "5m"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := run(t, tc.cmd(), srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), "--timeout only applies with --wait") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestWaitToleratesATransientError(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, activation(105, "DataStreaming"),
		reply{status: 500, body: `{"status":"error","code":500,"message":"boom","data":[]}`},
		activation(104, "Completed"))

	_, errb, err := run(t, activationsShowCommand(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("one 500 should not end the wait: %v", err)
	}
	if len(got.paths) != 3 || !strings.Contains(errb, "poll failed (1/3)") {
		t.Errorf("paths = %v, stderr = %q", got.paths, errb)
	}
}

func TestWaitGivesUpAfterThreeFailures(t *testing.T) {
	fast(t)
	boom := reply{status: 500, body: `{"status":"error","code":500,"message":"boom","data":[]}`}
	srv, got := stubSeq(t, activation(101, "Processing"), boom, boom, boom)

	_, _, err := run(t, activationsShowCommand(), srv, "501", "--wait")
	if err == nil || !strings.Contains(err.Error(), "giving up after 3") {
		t.Fatalf("err = %v", err)
	}
	if len(got.paths) != 4 {
		t.Errorf("expected 1 good + 3 failed polls, got %d", len(got.paths))
	}
}

func TestWaitAbortsAtOnceOnHardError(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, reply{status: 404, body: `{"status":"error","code":404,"message":"Activation not found.","data":[]}`})

	_, _, err := run(t, activationsShowCommand(), srv, "9999", "--wait")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
	if len(got.paths) != 1 {
		t.Errorf("a 404 must not be retried, got %d calls", len(got.paths))
	}
}

// Without --wait the new show command is the old one.
func TestShowWithoutWaitIsUnchanged(t *testing.T) {
	srv, got := stubSeq(t, activation(104, "Completed"))

	out, errb, err := run(t, activationsShowCommand(), srv, "501")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 1 || got.paths[0] != activationPath501 {
		t.Errorf("paths = %v", got.paths)
	}
	if !strings.HasPrefix(out, "id") || !strings.Contains(out, "Completed") || errb != "" {
		t.Errorf("stdout:\n%s\nstderr: %q", out, errb)
	}
}

// Staging activation 614, verbatim: Completed at the top, but its only stream
// failed and delivered nothing. --wait must not report success.
func TestWaitFailsWhenCompletedWithFailedDatastream(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, activation(105, "DataStreaming"), reply{body: `{"status":"success","code":200,"data":[
	 {"id":614,"description":"KLAX Visitors Activation","status":{"id":104,"name":"Completed"},
	  "datastreams":[{"name":"marketing_audience_maid_and_emails","status":"failed",
	   "error":"Reddit audience ca.2548235726874894631 reported status 'NOT_ENOUGH_MATCHES_ERROR' (matched size range [0, 0]; Reddit requires >= 1000 matched users for a valid audience)",
	   "results":{}}]}]}`})

	out, errb, err := run(t, activationsShowCommand(), srv, "614", "--wait")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	for _, want := range []string{"Completed, but 1 of 1 datastreams failed", "NOT_ENOUGH_MATCHES_ERROR"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q: %v", want, err)
		}
	}
	if !strings.Contains(errb, "Completed (104)") {
		t.Errorf("the status change should still be reported on stderr: %q", errb)
	}
	if out != "" {
		t.Errorf("stdout should stay clean on failure, got %q", out)
	}
}

// A stream that succeeded, or an older record reading "unknown", is not a failure.
func TestWaitCompletedWithHealthyDatastreamsSucceeds(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, reply{body: `{"status":"success","code":200,"data":[
	 {"id":625,"status":{"id":104,"name":"Completed"},
	  "datastreams":[{"name":"marketing_audience","status":"success","results":{"uri":"s3://intuizi-development/martin/x.csv.gz"}},
	                 {"name":"legacy","status":"unknown","results":{}}]}]}`})

	if _, _, err := run(t, activationsShowCommand(), srv, "625", "--wait"); err != nil {
		t.Fatalf("healthy streams should succeed: %v", err)
	}
}

// --dry-run is the one place to see what an activation will cost before it
// costs it: the body the flags produce, nothing sent, no token needed.
func TestActivationsCreateDryRunPrintsBodyAndSendsNothing(t *testing.T) {
	srv, got := stub(t, `{}`)

	out, _, err := run(t, activationsCreateCommand(), srv,
		append(threeIDs, "--description", "Q1 export", "--project-id", "4", "--dry-run")...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("dry run must send nothing, got %v", got.paths)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(out), &body); err != nil {
		t.Fatalf("stdout is not the JSON body: %v\n%s", err, out)
	}
	for k, want := range map[string]any{
		"audience_id": 88.0, "endpoint_connection_id": 12.0, "pricing_model_id": 3.0,
		"description": "Q1 export", "project_id": 4.0,
	} {
		if body[k] != want {
			t.Errorf("%s = %v, want %v", k, body[k], want)
		}
	}
}

func TestActivationsCreateDryRunRejectsWaitAndFile(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, activationsCreateCommand(), srv, append(threeIDs, "--dry-run", "--wait")...)
	if err == nil || !strings.Contains(err.Error(), "nothing to --wait for") {
		t.Errorf("--dry-run --wait: err = %v", err)
	}

	path := filepath.Join(t.TempDir(), "a.json")
	if err := os.WriteFile(path, []byte(`{"audience_id":88}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = run(t, activationsCreateCommand(), srv, "--file", path, "--dry-run")
	if err == nil || !strings.Contains(err.Error(), "--file already has one") {
		t.Errorf("--file --dry-run: err = %v", err)
	}

	var ue usageError
	if !errors.As(err, &ue) {
		t.Errorf("err = %v, want a usageError", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// A positional id of 0 is refused by parseID; the same id given as a flag
// must not slip through to a 422.
func TestActivationsCreateRejectsNonPositiveIDFlags(t *testing.T) {
	srv, got := stub(t, `{}`)

	for _, tc := range []struct{ flag, value string }{
		{"--audience-id", "0"},
		{"--endpoint-connection-id", "-1"},
		{"--pricing-model-id", "0"},
		{"--project-id", "0"},
	} {
		args := []string{"--audience-id", "88", "--endpoint-connection-id", "12", "--pricing-model-id", "3"}
		for i := 0; i < len(args); i += 2 {
			if args[i] == tc.flag {
				args[i+1] = tc.value
			}
		}
		if tc.flag == "--project-id" {
			args = append(args, tc.flag, tc.value)
		}

		_, _, err := run(t, activationsCreateCommand(), srv, args...)
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%s %s: err = %v, want a usageError", tc.flag, tc.value, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.flag) {
			t.Errorf("the error should name %s, got %v", tc.flag, err)
		}
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad id should cost no round trip, got %v", got.paths)
	}
}
