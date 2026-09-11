package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// waitShow is a show-like leaf built on the wait helpers alone, so these tests
// pin the helpers rather than whichever resource commands keep --wait.
func waitShow() *cobra.Command {
	cmd := &cobra.Command{Use: "show <id>", Args: cobra.ExactArgs(1)}
	readWait := waitFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		wait, timeout, err := readWait()
		if err != nil {
			return err
		}
		id, err := parseID(args[0], "activation")
		if err != nil {
			return err
		}
		c, err := client()
		if err != nil {
			return err
		}
		if !wait {
			return renderOne(cmd, "/analyses/activations/"+strconv.Itoa(id), nil)
		}
		return waitAndPrint(cmd, c, "/analyses/activations", "activation", id, nil, timeout)
	}
	return cmd
}

// A wait bounded by nothing used to report "timed out after 0s" with no
// request made. It is a bad flag value, so it is refused as one.
func TestWaitRejectsANonPositiveTimeout(t *testing.T) {
	srv, got := stubSeq(t, activation(101, "Processing"))

	for _, timeout := range []string{"0", "-1s"} {
		t.Run(timeout, func(t *testing.T) {
			_, _, err := run(t, waitShow(), srv, "501", "--wait", "--timeout", timeout)
			if err == nil || !strings.Contains(err.Error(), "--timeout") {
				t.Fatalf("err = %v, want one naming --timeout", err)
			}
			if code := exitCode(err, nil, true); code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
		})
	}
	if len(got.paths) != 0 {
		t.Errorf("a refused timeout should cost no round trip, got %v", got.paths)
	}
}

// hang answers nothing until the client gives up, so a deadline or a signal
// lands while the GET is in flight.
func hang(t *testing.T, onRequest func()) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if onRequest != nil {
			onRequest()
		}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestTimeoutBeforeAnyStatusSaysSo(t *testing.T) {
	srv := hang(t, nil)

	_, _, err := run(t, waitShow(), srv, "501", "--wait", "--timeout", "30ms")
	if !errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, want errWaitTimeout", err)
	}
	if !strings.Contains(err.Error(), "no status was read") {
		t.Errorf("error should say no status was read, got %v", err)
	}
	if strings.Contains(err.Error(), "still running") {
		t.Errorf("nothing was read, so nothing is known to be running: %v", err)
	}
	if !strings.Contains(err.Error(), "show 501 --wait") {
		t.Errorf("the resume hint should survive, got %v", err)
	}
}

// The two shapes of the timeout message, pinned side by side.
func TestTimeoutErrorNamesWhatWasSeen(t *testing.T) {
	seen := timeoutError("activation", 501, "Processing", 0).Error()
	if !strings.Contains(seen, "last status Processing") || !strings.Contains(seen, "still running") {
		t.Errorf("with a status: %s", seen)
	}
	unseen := timeoutError("activation", 501, "", 0).Error()
	if !strings.Contains(unseen, "no status was read") || strings.Contains(unseen, "still running") {
		t.Errorf("without one: %s", unseen)
	}
}

// A Ctrl-C while a poll is in flight used to print "poll failed (1/3),
// retrying: ... context canceled" on the way out. The exit code says it all.
func TestWaitCancelledMidPollIsQuiet(t *testing.T) {
	fast(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	srv := hang(t, cancel) // the signal lands once the GET is in flight

	cmd := waitShow()
	cmd.SetContext(ctx)
	_, errb, err := run(t, cmd, srv, "501", "--wait")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's own error", err)
	}
	if strings.Contains(errb, "poll failed") {
		t.Errorf("a cancelled poll is not a failed one:\n%s", errb)
	}
}

// After a successful wait, --json re-reads the record for the server's own
// bytes. One blip on that re-read used to turn the success into exit 1 with
// nothing printed.
func TestWaitJSONRetriesTheReRead(t *testing.T) {
	fast(t)
	boom := reply{status: 500, body: `{"status":"error","code":500,"message":"boom","data":[]}`}
	srv, got := stubSeq(t, activation(104, "Completed"), boom, activation(104, "Completed"))

	out, _, err := runJSON(t, waitShow(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("one failed re-read must not fail a finished wait: %v", err)
	}
	if len(got.paths) != 3 {
		t.Errorf("expected poll + failed re-read + retry, got %d calls", len(got.paths))
	}
	var env struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Code != 200 {
		t.Errorf("stdout should be the server envelope:\n%s", out)
	}
}

// When the re-read keeps failing, the last polled record is printed rather
// than nothing: the wait did succeed, and exit 1 would say the job failed.
func TestWaitJSONFallsBackToTheFinalRecord(t *testing.T) {
	fast(t)
	boom := reply{status: 500, body: `{"status":"error","code":500,"message":"boom","data":[]}`}
	srv, got := stubSeq(t, activation(104, "Completed"), boom)

	out, errb, err := runJSON(t, waitShow(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("a finished wait must not fail on the re-read: %v", err)
	}
	if want := 1 + maxPollFailures; len(got.paths) != want {
		t.Errorf("expected poll + %d re-read attempts, got %d calls", maxPollFailures, len(got.paths))
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(out), &rec); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out)
	}
	if status, _ := rec["status"].(map[string]any); status["name"] != "Completed" {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
	if !strings.Contains(errb, "last polled record") {
		t.Errorf("stderr should say the shape is the fallback, got %q", errb)
	}
}

// The re-read shares the poll's tolerance in both directions: a 404 is not
// retried there either, so the fallback comes at once rather than after two
// poll intervals.
func TestWaitJSONFallsBackAtOnceOnAHardError(t *testing.T) {
	fast(t)
	gone := reply{status: 404, body: `{"status":"error","code":404,"message":"Activation not found.","data":[]}`}
	srv, got := stubSeq(t, activation(104, "Completed"), gone)

	out, _, err := runJSON(t, waitShow(), srv, "501", "--wait")
	if err != nil {
		t.Fatalf("a finished wait must not fail on the re-read: %v", err)
	}
	if len(got.paths) != 2 {
		t.Errorf("expected poll + one re-read, got %d calls", len(got.paths))
	}
	if !strings.Contains(out, "Completed") {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
}
