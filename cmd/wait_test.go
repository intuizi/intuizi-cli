package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
)

// fallbackNote is the stderr line a --json wait prints when the re-read after
// it fails and the envelope it last polled is printed instead.
const fallbackNote = "printing the envelope as last polled:"

// polledEnvelope decodes --json wait output as the server's envelope and
// returns the status name of its one record, failing the test on any other
// shape: a script reads .data[0].status whatever the outcome.
func polledEnvelope(t *testing.T, out string) string {
	t.Helper()
	var env struct {
		Code int `json:"code"`
		Data []struct {
			Status struct {
				Name string `json:"name"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Code != 200 || len(env.Data) != 1 {
		t.Fatalf("stdout should be the server's envelope (%v):\n%s", err, out)
	}
	return env.Data[0].Status.Name
}

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
		return waitAndPrint(cmd, c, resourceLifecycle("activation", "/analyses/activations", nil), id, timeout)
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

	out, _, err := run(t, waitShow(), srv, "501", "--wait", "--timeout", "30ms")
	if !errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, want errWaitTimeout", err)
	}
	// Nothing was read, so there is no record to print.
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
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
	seen := timeoutError(activationLifecycle, 501, "Processing", 0).Error()
	if !strings.Contains(seen, "last status Processing") || !strings.Contains(seen, "still running") {
		t.Errorf("with a status: %s", seen)
	}
	unseen := timeoutError(activationLifecycle, 501, "", 0).Error()
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

// A signal during the --json re-read after a failed wait ends the command like
// a signal anywhere else in a wait: with the context's own error, not the
// wait's, and nothing half-printed on stdout.
func TestWaitCancelledDuringTheReReadReturnsTheContextError(t *testing.T) {
	fast(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var (
		mu sync.Mutex
		n  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		i := n
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch i {
		case 1:
			_, _ = w.Write([]byte(activation(101, "Processing").body))
		case 2:
			_, _ = w.Write([]byte(failedActivation.body))
		default:
			cancel() // the signal lands once the re-read is in flight
			<-r.Context().Done()
		}
	}))
	t.Cleanup(srv.Close)

	cmd := waitShow()
	cmd.SetContext(ctx)
	out, _, err := runJSON(t, cmd, srv, "501", "--wait")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context's own error", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
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

// When the re-read keeps failing, the envelope last polled is printed rather
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
	if name := polledEnvelope(t, out); name != "Completed" {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
	if !strings.Contains(errb, fallbackNote+" re-reading it failed (") {
		t.Errorf("stderr should say the shape is the fallback, and why, got %q", errb)
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
	if name := polledEnvelope(t, out); name != "Completed" {
		t.Errorf("stdout should be the final record:\n%s", out)
	}
}

// The ids a wait polls through, checked against the worker and the console:
// 100-103 and 105 are build and export progress, 108 is a Lookalike Model
// training and 109 an audience drawing its data stream visualizations. 104 is
// the one success. Everything else ends the wait - 106 Expired, 107 Additional
// Info and the 4xx errors - and so does any id this list does not name.
func TestWaitingSetIsExactlyTheNonTerminalIDs(t *testing.T) {
	want := map[int]bool{100: true, 101: true, 102: true, 103: true, 105: true, 108: true, 109: true}
	for id := 100; id <= 109; id++ {
		if waiting[id] != want[id] {
			t.Errorf("waiting[%d] = %v, want %v", id, waiting[id], want[id])
		}
	}
	for id := 400; id <= 405; id++ {
		if waiting[id] {
			t.Errorf("waiting[%d] = true, but a 4xx is terminal", id)
		}
	}
	if len(waiting) != len(want) {
		t.Errorf("waiting has %d ids, want exactly %d: %v", len(waiting), len(want), waiting)
	}
}

// createdAudience1377 is the create reply the audience wait tests start from.
var createdAudience1377 = reply{body: `{"status":"success","code":201,"data":[{"id":1377,"name":"CLI demo - LAX visitors","status":{"id":100,"name":"Initiating"},"results_count":0}]}`}

// An audience that opts into data stream visualizations reports 109 while it
// draws them, then 105 and 104. The wait used to fail on 109 with the build
// still running.
func TestAudienceWaitWaitsThroughVisualizing(t *testing.T) {
	visualizing := audience(109, "Visualizing data streams")
	for name, tc := range map[string]struct {
		cmd     func() *cobra.Command
		args    func(t *testing.T) []string
		replies []reply
	}{
		"create": {audiencesCreateCommand,
			func(t *testing.T) []string {
				return []string{"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"POI"}]}`), "--wait", "--timeout", "5s"}
			},
			[]reply{createdAudience1377, audience(101, "Processing"), visualizing, visualizing,
				audience(105, "DataStreaming"), audience(104, "Completed")}},
		"show": {audiencesShowCommand,
			func(*testing.T) []string { return []string{"1377", "--wait", "--timeout", "5s"} },
			[]reply{visualizing, visualizing, audience(105, "DataStreaming"), audience(104, "Completed")}},
	} {
		t.Run(name, func(t *testing.T) {
			fast(t)
			srv, got := stubSeq(t, tc.replies...)

			out, errb, err := run(t, tc.cmd(), srv, tc.args(t)...)
			if err != nil {
				t.Fatalf("109 is not an ending: %v\nstderr:\n%s", err, errb)
			}
			if len(got.paths) != len(tc.replies) {
				t.Errorf("expected %d calls, got %d", len(tc.replies), len(got.paths))
			}
			if n := strings.Count(errb, "Visualizing data streams (109)"); n != 1 {
				t.Errorf("109 reported %d times, want once:\n%s", n, errb)
			}
			if !strings.Contains(errb, "Completed (104)") {
				t.Errorf("stderr should end at Completed:\n%s", errb)
			}
			if !strings.HasPrefix(out, "id") || !strings.Contains(out, "Completed") {
				t.Errorf("stdout should be the completed record:\n%s", out)
			}
		})
	}
}

// 107 Additional Info is the last status the worker posts when it cannot run a
// request as given (an InfoException): it records the reason and drops the job,
// and nothing follows. The wait stops there with exit 1 and the record, says
// where the reason can be read, and offers no command to resume: a later
// show --wait would stop on the same status at once.
func TestWaitStopsOnAdditionalInfo(t *testing.T) {
	stoppedAudience := audience(107, "Additional Info")
	stoppedActivation := activation(107, "Additional Info")
	for name, tc := range map[string]struct {
		cmd      func() *cobra.Command
		args     func(t *testing.T) []string
		replies  []reply
		singular string
		work     string
	}{
		"audiences create": {audiencesCreateCommand,
			func(t *testing.T) []string {
				return []string{"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"POI"}]}`), "--wait", "--timeout", "1s"}
			},
			[]reply{createdAudience1377, audience(101, "Processing"), stoppedAudience}, "audience 1377", "build"},
		"audiences show": {audiencesShowCommand,
			func(*testing.T) []string { return []string{"1377", "--wait", "--timeout", "1s"} },
			[]reply{audience(102, "Analyzing"), stoppedAudience}, "audience 1377", "build"},
		"activations create": {activationsCreateCommand,
			func(*testing.T) []string {
				return append(append([]string(nil), threeIDs...), "--datastream", "7", "--wait", "--timeout", "1s")
			},
			[]reply{createdActivation, activation(101, "Processing"), stoppedActivation}, "activation 501", "export"},
		"activations show": {activationsShowCommand,
			func(*testing.T) []string { return []string{"501", "--wait", "--timeout", "1s"} },
			[]reply{activation(103, "Decryption Requested"), stoppedActivation}, "activation 501", "export"},
	} {
		t.Run(name, func(t *testing.T) {
			fast(t)
			srv, got := stubSeq(t, tc.replies...)

			out, errb, err := run(t, tc.cmd(), srv, tc.args(t)...)
			if !errors.Is(err, errWaitFailed) {
				t.Fatalf("err = %v, want errWaitFailed", err)
			}
			if code := exitCode(err, nil, true); code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			// No poll after 107: nothing follows it.
			if len(got.paths) != len(tc.replies) {
				t.Errorf("expected %d calls, got %d - the wait went on past 107", len(tc.replies), len(got.paths))
			}
			msg := err.Error()
			for _, want := range []string{tc.singular + " failed",
				"the " + tc.work + " stopped with Additional Info (107)",
				"the API does not return the reason", "Intuizi console"} {
				if !strings.Contains(msg, want) {
					t.Errorf("error missing %q: %s", want, msg)
				}
			}
			for _, never := range []string{"--wait", "rerun", "still running", "keep waiting"} {
				if strings.Contains(msg, never) {
					t.Errorf("107 is over, so the error must not say %q: %s", never, msg)
				}
			}
			if strings.Contains(errb, "--wait") {
				t.Errorf("stderr must not suggest waiting again:\n%s", errb)
			}
			if strings.HasPrefix(tc.singular, "audience") != strings.Contains(msg, "date range") {
				t.Errorf("only an audience build hints at its date range: %s", msg)
			}
			if !strings.HasPrefix(out, "id") || !strings.Contains(out, "Additional Info") {
				t.Errorf("stdout should be the record the wait stopped on:\n%s", out)
			}
		})
	}
}

// With --json a wait that stops on 107 prints the server's envelope, read once
// more like any failed wait, and still exits 1.
func TestWaitJSONStopsOnAdditionalInfo(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, audience(101, "Processing"), audience(107, "Additional Info"))

	out, _, err := runJSON(t, audiencesShowCommand(), srv, "1377", "--wait", "--timeout", "1s")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	// Two polls and the one re-read a failed wait gets.
	if len(got.paths) != 3 {
		t.Errorf("expected 3 calls, got %d", len(got.paths))
	}
	var env struct {
		Code int `json:"code"`
		Data []struct {
			Status struct {
				ID int `json:"id"`
			} `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Code != 200 || len(env.Data) != 1 {
		t.Fatalf("stdout should be the server envelope (%v):\n%s", err, out)
	}
	if env.Data[0].Status.ID != 107 {
		t.Errorf(".data[0].status.id = %d, want 107", env.Data[0].Status.ID)
	}
}
