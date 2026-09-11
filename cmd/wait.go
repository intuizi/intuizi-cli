package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// pollInterval is how long --wait sleeps between reads. A var, not a const, so
// tests can shrink it to a millisecond.
var pollInterval = 15 * time.Second

// maxPollFailures is how many consecutive failed reads --wait tolerates before
// giving up. A 60-minute wait will meet a blip, and one bad read should not
// throw away 40 minutes of waiting. Any good read resets the count.
const maxPollFailures = 3

// defaultWaitTimeout bounds a --wait so a CI job cannot hang forever.
const defaultWaitTimeout = 60 * time.Minute

// statusCompleted is the one terminal success id.
const statusCompleted = 104

// waiting holds every lifecycle id that means "not finished yet". 105
// DataStreaming belongs here: the API reports it BEFORE 104 Completed. Anything
// that is neither waiting nor 104 ends the wait as a failure - 106 Expired, the
// 4xx error states, and any id this CLI has never heard of - so an unknown code
// can never hang a CI job for the full timeout.
var waiting = map[int]bool{
	100: true, // Initiating
	101: true, // Processing
	102: true, // Analyzing
	103: true, // Decryption Requested
	105: true, // DataStreaming
	107: true, // Additional Info
	108: true, // Modeling
}

// Sentinel causes, so a later UX layer can map them to distinct exit codes
// without touching this file. Test with errors.Is.
var (
	errWaitFailed  = errors.New("failed")
	errWaitTimeout = errors.New("timed out")
)

// waitFlags registers --wait and --timeout on cmd and returns a reader that
// validates them. --timeout without --wait is rejected rather than ignored: a
// silently ignored flag hides a typo in a CI script.
func waitFlags(cmd *cobra.Command) func() (bool, time.Duration, error) {
	var (
		wait    bool
		timeout time.Duration
	)
	flags := cmd.Flags()
	flags.BoolVar(&wait, "wait", false,
		"Poll until it completes or fails; exit non-zero on failure")
	flags.DurationVar(&timeout, "timeout", defaultWaitTimeout,
		"Give up waiting after this long (with --wait)")

	return func() (bool, time.Duration, error) {
		if !wait && flags.Changed("timeout") {
			return false, 0, usageErr("--timeout only applies with --wait")
		}
		if wait && timeout <= 0 {
			// Otherwise the wait "times out" before its first read.
			return false, 0, usageErr(fmt.Sprintf(
				"--timeout %s cannot bound a wait - pass a positive duration such as 30m", timeout))
		}
		return wait, timeout, nil
	}
}

// waitAndPrint follows one resource to a terminal status and prints the final
// state. With --json the envelope is re-read so the bytes printed are the
// server's own, as every other --json path does.
func waitAndPrint(cmd *cobra.Command, c *api.Client, prefix, singular string, id int, cols []string, timeout time.Duration) error {
	path := prefix + "/" + strconv.Itoa(id)
	final, err := waitFor(cmd, c, path, singular, id, timeout)
	if quietOutput {
		// The id exists either way; the exit code carries the outcome.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), id)
		return err
	}
	if err != nil {
		return err
	}
	if jsonOutput {
		raw, err := rawAfterWait(cmd, c, path, final)
		if err != nil {
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}
	return output.Detail(cmd.OutOrStdout(), flatten(final), cols)
}

// rawAfterWait re-reads the finished record for --json. The wait has already
// succeeded, so a blip here is retried with the poll's own tolerance and,
// failing that, the last polled record is printed instead: exit 1 with
// nothing on stdout would read as the job having failed.
func rawAfterWait(cmd *cobra.Command, c *api.Client, path string, final output.Record) ([]byte, error) {
	ctx := cmd.Context()
	var err error
	for attempt := 1; ; attempt++ {
		var raw []byte
		if raw, err = c.GetRaw(ctx, path, nil); err == nil {
			return raw, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if attempt >= maxPollFailures || isHardError(err) {
			break
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, err
		}
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
		"re-reading the final state failed (%v) - printing the last polled record instead\n", err)
	return json.MarshalIndent(final, "", "  ")
}

// createAndWait posts a body, reports the new id on stderr, then follows it.
// stdout carries only the final state, so --json emits one document.
func createAndWait(cmd *cobra.Command, prefix, singular string, body any, cols []string, timeout time.Duration) error {
	c, err := client()
	if err != nil {
		return err
	}
	created, err := createRecord(cmd, c, prefix+"/create", body)
	if err != nil {
		return err
	}
	id, err := idOf(created)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "created %s %d\n", singular, id)
	return waitAndPrint(cmd, c, prefix, singular, id, cols, timeout)
}

// waitFor polls GET path until the resource's lifecycle status is terminal.
// Each status change goes to stderr; the final record is returned for the
// caller to print on stdout. singular ("activation") and id only feed messages.
func waitFor(cmd *cobra.Command, c *api.Client, path, singular string, id int, timeout time.Duration) (output.Record, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	var (
		rec      output.Record
		last     = -1
		lastName string
		failures int
	)
	for {
		next, err := api.Read[output.Record](ctx, c, path, nil)
		switch {
		case err == nil:
			failures = 0
			rec = next
			sid, name := statusOf(rec)
			if sid != last {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s %d: %s\n", singular, id, statusLabel(sid, name))
				last, lastName = sid, name
			}
			if sid == statusCompleted {
				// Completed says the job finished, not that it delivered: staging
				// activation 614 reads 104 with its only stream failed and results {}.
				if failed, total, detail := failedDatastreams(rec); failed > 0 {
					return rec, fmt.Errorf("%s %d %w: Completed, but %d of %d datastreams failed%s",
						singular, id, errWaitFailed, failed, total, detail)
				}
				return rec, nil
			}
			if !waiting[sid] {
				_, _, detail := failedDatastreams(rec)
				return rec, fmt.Errorf("%s %d %w: %s%s", singular, id, errWaitFailed,
					statusLabel(sid, name), detail)
			}

		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return rec, timeoutError(singular, id, lastName, timeout)

		case ctx.Err() != nil:
			// Ctrl-C or SIGTERM landed mid-request. Execute maps it to the
			// signal's code; a "retrying" line would be noise on the way out.
			return rec, ctx.Err()

		case isHardError(err):
			// 401, 403, 404: retrying cannot help.
			return rec, err

		default:
			failures++
			if failures >= maxPollFailures {
				return rec, fmt.Errorf("giving up after %d consecutive failed reads: %w", failures, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "poll failed (%d/%d), retrying: %v\n", failures, maxPollFailures, err)
		}

		if err := sleepCtx(ctx, pollInterval); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return rec, timeoutError(singular, id, lastName, timeout)
			}
			return rec, err
		}
	}
}

// timeoutError names the id, the last status seen and the exact command that
// resumes the wait - the two things a CI log needs. The job keeps running
// server-side; a timeout is the CLI giving up, not the export.
func timeoutError(singular string, id int, lastName string, timeout time.Duration) error {
	// With no read at all, nothing is known to be running.
	seen := "no status was read"
	if lastName != "" {
		seen = fmt.Sprintf("last status %s - it is still running", lastName)
	}
	return fmt.Errorf("%w after %s waiting for %s %d: %s; "+
		"rerun 'intuizi %ss show %d --wait' to keep waiting",
		errWaitTimeout, timeout, singular, id, seen, singular, id)
}

// statusOf reads {id, name} out of the status object. Numbers are json.Number
// because output.Record decodes with UseNumber; a float64 assertion would yield
// 0 and every poll would look the same. A record with no status object reads as
// id 0, which is not in the waiting set, so the caller fails rather than waits.
func statusOf(rec output.Record) (int, string) {
	m, _ := rec["status"].(map[string]any)
	n, _ := m["id"].(json.Number)
	sid, _ := strconv.Atoi(n.String())
	name, _ := m["name"].(string)
	return sid, name
}

func statusLabel(sid int, name string) string {
	if name == "" {
		return fmt.Sprintf("status %d", sid)
	}
	return fmt.Sprintf("%s (%d)", name, sid)
}

// failedDatastreams counts the streams that did not deliver and collects their
// error strings for the failure message. A stream has failed when its status
// is "failed" or it carries an error; "success", and "unknown" on older
// records, are not failures. The lifecycle status alone cannot say whether
// anything was delivered - the per-stream error is the only place the API says why.
func failedDatastreams(rec output.Record) (failed, total int, detail string) {
	streams, _ := rec["datastreams"].([]any)
	for _, s := range streams {
		m, _ := s.(map[string]any)
		total++
		status, _ := m["status"].(string)
		e, _ := m["error"].(string)
		if status != "failed" && e == "" {
			continue
		}
		failed++
		if e == "" {
			e = "no error message"
		}
		name, _ := m["name"].(string)
		detail += fmt.Sprintf("\n  %s: %s", name, e)
	}
	return failed, total, detail
}

// isHardError reports an API error that retrying cannot fix.
func isHardError(err error) bool {
	var apiErr *api.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	}
	return false
}

// sleepCtx waits for d unless ctx ends first. time.Sleep would ignore the
// --timeout deadline and hold a Ctrl-C for the whole interval.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
