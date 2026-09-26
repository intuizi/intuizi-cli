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

// statusAdditionalInfo is the worker's InfoException: it could not run the
// request as given, so it posts the reason as the status message, with this
// id, and drops the job. No status follows it, so it ends a wait as a failure,
// and one that no later 'show --wait' can resume.
const statusAdditionalInfo = 107

// waiting holds every lifecycle id that means "not finished yet", checked
// against the worker and the console. 105 DataStreaming and 109 Visualizing
// data streams belong here: the worker posts both BEFORE 104 Completed, 109
// while an audience draws its data stream visualizations. Anything that is
// neither waiting nor 104 ends the wait as a failure - 106 Expired, 107
// Additional Info, the 4xx error states, and any id this CLI has never heard
// of - so an unknown code can never hang a CI job for the full timeout. That
// includes the console's deprecated 200 and 201: no worker posts them now, so
// a record still reading one will never move.
var waiting = map[int]bool{
	100: true, // Initiating
	101: true, // Processing
	102: true, // Analyzing
	103: true, // Decryption Requested
	105: true, // DataStreaming
	108: true, // Modeling
	109: true, // Visualizing data streams
}

// Sentinel causes, so a later UX layer can map them to distinct exit codes
// without touching this file. Test with errors.Is.
var (
	errWaitFailed  = errors.New("failed")
	errWaitTimeout = errors.New("timed out")
	errWaitGaveUp  = errors.New("giving up")
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
		"Poll until it completes or fails, then print the record;\nexit non-zero on failure or timeout")
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

// waitAndPrint follows one resource until its wait ends and prints the record
// the wait ended on: the terminal record when it completed or failed, the
// last one polled when it timed out or gave up. With --json a completed or
// failed record is re-read so the bytes printed are the server's own, as
// every other --json path does; an abandoned wait prints the envelope as last
// polled, which holds the record its error names. Either way --json prints
// the server's envelope, so .data[0].status reads the same on every outcome.
//
// A wait that fails, times out or gives up after repeated failed reads still
// prints the last record read, then returns its error, so the exit code is
// unchanged and a CI log shows where the job stood. A hard API error or a
// signal prints no record: it may be long stale, and the error says what went
// wrong. With --json a refusal still prints its error envelope.
func waitAndPrint(cmd *cobra.Command, c *api.Client, prefix, singular string, id int, cols []string, timeout time.Duration) error {
	path := prefix + "/" + strconv.Itoa(id)
	final, raw, err := waitFor(cmd, c, path, singular, id, timeout)
	if quietOutput {
		// The id exists either way; the exit code carries the outcome.
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), id)
		return err
	}
	if err != nil && (final == nil || !printsRecord(err)) {
		if jsonOutput {
			printErrorEnvelope(cmd, err)
		}
		return err
	}
	perr := printWaited(cmd, c, path, final, raw, cols, err)
	if perr != nil && (err == nil || cmd.Context().Err() != nil) {
		// A signal during the --json re-read ends the command with the
		// context's error, as it does anywhere else in a wait.
		return perr
	}
	return err
}

// printsRecord reports a wait error that still ends on a record worth
// printing: a failed state, or a wait abandoned - timed out or given up -
// while the job runs on.
func printsRecord(err error) bool {
	return errors.Is(err, errWaitFailed) || errors.Is(err, errWaitTimeout) || errors.Is(err, errWaitGaveUp)
}

// printWaited prints the record a wait ended on, in the active output mode.
// polled is the envelope final came in. waitErr is how the wait ended, nil
// for a success.
func printWaited(cmd *cobra.Command, c *api.Client, path string, final output.Record, polled []byte, cols []string, waitErr error) error {
	if jsonOutput {
		raw := polled
		// A timeout prints the envelope as polled: a read after the deadline
		// would overrun --timeout, and could show a later status than the one
		// the timeout error names. So does giving up: the last three reads
		// failed, and a fourth would only delay the exit.
		if !errors.Is(waitErr, errWaitTimeout) && !errors.Is(waitErr, errWaitGaveUp) {
			// After a failure the exit code is 1 whatever this read does, so
			// it is not worth up to two more poll intervals.
			attempts := maxPollFailures
			if waitErr != nil {
				attempts = 1
			}
			var err error
			if raw, err = rawAfterWait(cmd, c, path, polled, attempts); err != nil {
				return err
			}
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}
	return output.Detail(cmd.OutOrStdout(), flatten(final), cols)
}

// rawAfterWait re-reads the record a wait ended on, for --json, in up to
// attempts tries. The wait is over whatever its outcome, so when the re-read
// keeps failing the envelope as last polled is printed instead: after a
// success, exit 1 with nothing on stdout would read as the job having failed.
func rawAfterWait(cmd *cobra.Command, c *api.Client, path string, polled []byte, attempts int) ([]byte, error) {
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
		if attempt >= attempts || isHardError(err) {
			break
		}
		if err := sleepCtx(ctx, pollInterval); err != nil {
			return nil, err
		}
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "printing the envelope as last polled: re-reading it failed (%v)\n", err)
	return polled, nil
}

// createAndWait posts a body, reports the new id on stderr, then follows it.
// stdout carries only the state the wait ended on, so --json emits one
// document.
func createAndWait(cmd *cobra.Command, prefix, singular string, body any, cols []string, timeout time.Duration) error {
	c, err := client()
	if err != nil {
		return err
	}
	created, err := createRecord(cmd, c, prefix+"/create", body)
	if err != nil {
		// Nothing was created, so there is no record to wait on: --json gets
		// the error envelope, as it does from a create without --wait.
		if jsonOutput {
			printErrorEnvelope(cmd, err)
		}
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
// Each status change goes to stderr; the last record read is returned, nil if
// none was, with the envelope it came in and how the wait ended, for the
// caller to print on stdout. singular and id feed messages; singular
// "activation" also turns on the stderr note for a Completed record with no
// datastreams.
func waitFor(cmd *cobra.Command, c *api.Client, path, singular string, id int, timeout time.Duration) (output.Record, []byte, error) {
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()

	// rec and raw move together: every return hands back the last good read
	// and the envelope it came in.
	var (
		rec      output.Record
		raw      []byte
		last     = -1
		lastName string
		failures int
	)
	for {
		next, body, err := api.ReadRaw[output.Record](ctx, c, path, nil)
		switch {
		case err == nil:
			failures = 0
			rec, raw = next, body
			sid, name := statusOf(rec)
			if sid != last {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s %d: %s\n", singular, id, statusLabel(sid, name))
				last, lastName = sid, name
			}
			if sid == statusCompleted {
				// Completed says the job finished, not that it delivered: staging
				// activation 614 reads 104 with its only stream failed and results {}.
				if failed, total, detail := failedDatastreams(rec); failed > 0 {
					return rec, raw, fmt.Errorf("%s %d %w: Completed, but %d of %d datastreams failed%s",
						singular, id, errWaitFailed, failed, total, detail)
				}
				if singular == "activation" && noDatastreams(rec) {
					// Not a failure - nothing was asked to deliver - but not
					// the success a bare Completed reads as either.
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
						"note: %s %d has no datastreams, so nothing was delivered\n", singular, id)
				}
				return rec, raw, nil
			}
			if !waiting[sid] {
				_, _, detail := failedDatastreams(rec)
				if sid == statusAdditionalInfo {
					return rec, raw, additionalInfoError(singular, id, statusLabel(sid, name), detail)
				}
				return rec, raw, fmt.Errorf("%s %d %w: %s%s", singular, id, errWaitFailed,
					statusLabel(sid, name), detail)
			}

		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			return rec, raw, timeoutError(singular, id, lastName, timeout)

		case ctx.Err() != nil:
			// Ctrl-C or SIGTERM landed mid-request. Execute maps it to the
			// signal's code; a "retrying" line would be noise on the way out.
			return rec, raw, ctx.Err()

		case isHardError(err):
			// 401, 403, 404: retrying cannot help.
			return rec, raw, err

		default:
			failures++
			if failures >= maxPollFailures {
				return rec, raw, gaveUpError(singular, id, failures, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "poll failed (%d/%d), retrying: %v\n", failures, maxPollFailures, err)
		}

		if err := sleepCtx(ctx, pollInterval); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return rec, raw, timeoutError(singular, id, lastName, timeout)
			}
			return rec, raw, err
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

// gaveUpError is timeoutError's twin for a wait abandoned after reads kept
// failing, such as during a short API outage. The job was not seen to end, so
// it names the command that resumes the wait, as a timeout does.
func gaveUpError(singular string, id, failures int, err error) error {
	return fmt.Errorf("%w after %d consecutive failed reads: %w; the %s may still be running - "+
		"rerun 'intuizi %ss show %d --wait' to keep waiting once the API answers again",
		errWaitGaveUp, failures, err, singular, singular, id)
}

// additionalInfoError explains a wait that ended on 107 Additional Info. The
// worker records why as the status message, which the v2 read does not return,
// so the error says where the reason can be read and, for an audience, what
// usually causes it. It names no command to resume: the job is over, and a
// later 'show --wait' would stop on the same status at once.
func additionalInfoError(singular string, id int, label, detail string) error {
	work, next := "export", "fix what it names and create a new activation"
	if singular == "audience" {
		work, next = "build", "it is most often a date range outside the dataset's data coverage, "+
			"or a filter the dataset needs - fix the request and create a new audience"
	}
	return fmt.Errorf("%s %d %w: the %s stopped with %s and will not continue; "+
		"the API does not return the reason, but Audience Manager in the Intuizi console shows it on the %s; %s%s",
		singular, id, errWaitFailed, work, label, singular, next, detail)
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

// noDatastreams reports a record that lists its datastreams and has none. A
// record without the field says nothing either way.
func noDatastreams(rec output.Record) bool {
	streams, ok := rec["datastreams"].([]any)
	return ok && len(streams) == 0
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
