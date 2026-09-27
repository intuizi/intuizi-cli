package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// estimate is a read of estimate 12 in the documented shape: status is a bare
// string, not the {id, name} object an audience carries, and the numbers sit
// in the estimate object, which is null until the run completes.
func estimate(status, inner, statusMessage string) reply {
	msg := "null"
	if statusMessage != "" {
		msg = fmt.Sprintf("%q", statusMessage)
	}
	return reply{body: fmt.Sprintf(`{"status":"success","code":200,"message":"Resource fetched successfully.","data":[
	 {"id":12,"status":%q,"name":"Coffee Buyers NYC (probe)",
	  "recipe_hash":"sha256:5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
	  "normalized_payload":{"name":"Coffee Buyers NYC (probe)","operator":"Single","datasets":[{"cohort_id":7,"type":"Cohorts"}]},
	  "estimate":%s,"status_message":%s,"project_id":null,
	  "created_at":"2026-08-24 12:00:00","updated_at":"2026-08-24 12:07:00"}]}`, status, inner, msg)}
}

// completedNumbers is the estimate object of a completed run, as documented.
const completedNumbers = `{"uniques":482311,"visits":1203440,"signals":null,"unique_eips":null,
 "number_of_days":30,"method":"approx_distinct","exact":false,"as_of":"2026-08-24T12:34:56Z",
 "providers":["BID001","BID002"],
 "query_metrics":{"total_queries":9,"total_data_scanned_bytes":1073741824,"total_runtime_ms":412000},
 "canonical_hash":"sha256:5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
 "totals":{"uniques":482311,"visits":1203440},"blocked":null}`

// blockedNumbers is a blocked run: no numbers, and a code with the reason.
const blockedNumbers = `{"uniques":null,"blocked":{"code":"OUT_OF_COVERAGE",
 "reason":"No POI signal data is available for 2020-01-01..2020-02-01. POI data is available 2024-01 to 2026-07."}}`

const estimatePath12 = "/api/v2/analyses/audiences/estimate/12"

// The figures are the point of an estimate, so they are shown as rows of their
// own rather than as the {...} cell the nested estimate object would render as.
func TestEstimateShowPrintsTheFigures(t *testing.T) {
	srv, got := stubSeq(t, estimate("completed", completedNumbers, ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != estimatePath12 {
		t.Errorf("path = %s, want %s", got.paths[0], estimatePath12)
	}
	for _, want := range []string{"uniques", "482311", "visits", "1203440", "completed",
		"2026-08-24T12:34:56Z", "BID001, BID002", "approx_distinct"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "estimate ") {
		t.Errorf("the nested estimate object is still a row of its own:\n%s", out)
	}
	// The figures lead, ahead of the alphabetical rest.
	if strings.Index(out, "uniques") > strings.Index(out, "created_at") {
		t.Errorf("uniques should come before the alphabetical fields:\n%s", out)
	}
}

// A queued estimate has no numbers yet: the record still prints.
func TestEstimateShowPendingPrintsTheStatus(t *testing.T) {
	srv, _ := stubSeq(t, estimate("pending", "null", ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, "pending") {
		t.Errorf("stdout should show the status:\n%s", out)
	}
	if strings.Contains(out, "uniques") {
		t.Errorf("a pending estimate has no figures to show:\n%s", out)
	}
}

// A blocked estimate says why in estimate.blocked, which would otherwise be a
// {...} cell two levels down.
func TestEstimateShowBlockedShowsTheReason(t *testing.T) {
	srv, _ := stubSeq(t, estimate("blocked", blockedNumbers, ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12")
	if err != nil {
		t.Fatalf("a read of a blocked estimate is still a successful read: %v", err)
	}
	if !strings.Contains(out, "OUT_OF_COVERAGE: No POI signal data is available") {
		t.Errorf("stdout should carry the blocked code and reason:\n%s", out)
	}
}

func TestEstimateShowJSONPrintsTheEnvelope(t *testing.T) {
	srv, _ := stubSeq(t, estimate("completed", completedNumbers, ""))

	out, _, err := runJSON(t, estimateShowCommand(), srv, "12")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"message": "Resource fetched successfully."`) ||
		!strings.Contains(out, `"total_data_scanned_bytes": 1073741824`) {
		t.Errorf("--json should print the server's envelope untouched:\n%s", out)
	}
}

func TestEstimateShowRejectsABadIDBeforeSending(t *testing.T) {
	srv, got := stubSeq(t, estimate("completed", completedNumbers, ""))

	_, _, err := run(t, estimateShowCommand(), srv, "twelve")
	if code := exitCode(err, nil, true); code != 2 {
		t.Errorf("exit = %d, want 2 (err %v)", code, err)
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad id should cost no round trip, got %v", got.paths)
	}
}

// Estimates walk pending -> processing -> completed, as strings.
func TestEstimateShowWaitFollowsToCompleted(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t,
		estimate("pending", "null", ""), estimate("processing", "null", ""),
		estimate("processing", "null", ""), estimate("completed", completedNumbers, ""))

	out, errb, err := run(t, estimateShowCommand(), srv, "12", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 4 {
		t.Errorf("want 4 polls, got %v", got.paths)
	}
	for _, want := range []string{"estimate 12: pending", "estimate 12: processing", "estimate 12: completed"} {
		if strings.Count(errb, want) != 1 {
			t.Errorf("stderr should carry %q once:\n%s", want, errb)
		}
	}
	if !strings.Contains(out, "482311") || strings.Contains(out, "pending") {
		t.Errorf("stdout should be the completed record alone:\n%s", out)
	}
}

// blocked is final: the recipe cannot be answered, so there is no number to
// wait for, and the error carries the code and the reason.
func TestEstimateWaitEndsOnBlocked(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, estimate("processing", "null", ""), estimate("blocked", blockedNumbers, ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12", "--wait")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	for _, want := range []string{"estimate 12", "OUT_OF_COVERAGE", "POI data is available 2024-01 to 2026-07"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %q: %v", want, err)
		}
	}
	// Nothing to resume: waiting again would stop on the same status.
	if strings.Contains(err.Error(), "--wait") {
		t.Errorf("a final status should name no command to resume: %v", err)
	}
	if !strings.Contains(out, "blocked") {
		t.Errorf("the blocked record should still print:\n%s", out)
	}
}

func TestEstimateWaitEndsOnFailed(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, estimate("failed", "null", "Athena query exceeded the time limit"))

	_, _, err := run(t, estimateShowCommand(), srv, "12", "--wait")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	if !strings.Contains(err.Error(), "Athena query exceeded the time limit") {
		t.Errorf("error should carry the status_message: %v", err)
	}
}

// A status this CLI has never heard of ends the wait, as on the id scale,
// rather than holding a CI job for the whole timeout.
func TestEstimateWaitEndsOnAnUnknownStatus(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, estimate("archived", "null", ""))

	_, _, err := run(t, estimateShowCommand(), srv, "12", "--wait", "--timeout", "5s")
	if !errors.Is(err, errWaitFailed) {
		t.Fatalf("err = %v, want errWaitFailed", err)
	}
	if !strings.Contains(err.Error(), "archived") {
		t.Errorf("error should name the status: %v", err)
	}
	if len(got.paths) != 1 {
		t.Errorf("an unknown status should end the wait at once, got %d reads", len(got.paths))
	}
}

// The resume hint names the command that reads estimates, not a nonexistent
// 'intuizi estimates show'.
func TestEstimateWaitTimeoutNamesTheResumeCommand(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, estimate("processing", "null", ""))

	_, _, err := run(t, estimateShowCommand(), srv, "12", "--wait", "--timeout", "30ms")
	if !errors.Is(err, errWaitTimeout) {
		t.Fatalf("err = %v, want errWaitTimeout", err)
	}
	if !strings.Contains(err.Error(), "'intuizi audiences estimate show 12 --wait'") {
		t.Errorf("error should name the resume command: %v", err)
	}
	if !strings.Contains(err.Error(), "last status processing") {
		t.Errorf("error should name the last status: %v", err)
	}
}

func TestEstimateWaitJSONPrintsTheFinalEnvelope(t *testing.T) {
	fast(t)
	srv, _ := stubSeq(t, estimate("processing", "null", ""), estimate("completed", completedNumbers, ""))

	out, _, err := runJSON(t, estimateShowCommand(), srv, "12", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"status": "completed"`) || !strings.Contains(out, `"uniques": 482311`) {
		t.Errorf("--json should print the completed envelope:\n%s", out)
	}
}

func TestEstimateWaitQuietPrintsTheID(t *testing.T) {
	fast(t)
	quiet(t)
	srv, _ := stubSeq(t, estimate("completed", completedNumbers, ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12", "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "12\n" {
		t.Errorf("stdout = %q, want the id alone", out)
	}
}

// createdEstimate is the 201 an estimate create answers with: queued, and no
// figures yet.
var createdEstimate = reply{body: `{"status":"success","code":201,"message":"Resource created successfully.","data":[
 {"id":12,"status":"pending","name":"SF visitors",
  "recipe_hash":"sha256:5e884898da28047151d0e56f8dc6292773603d0d6aabbdd62a11ef721d1542d8",
  "normalized_payload":{"name":"SF visitors","operator":"Single","datasets":[{"type":"POI"}]},
  "estimate":null,"status_message":null,"project_id":null,
  "created_at":"2026-08-24 12:00:00","updated_at":"2026-08-24 12:00:00"}]}`}

const estimateCreatePath = "/api/v2/analyses/audiences/estimate"

// An estimate takes the Create Audience body, so the same flags must build the
// same body - and send it to the estimate endpoint, never to /create.
func TestEstimateCreateSendsTheAudienceBody(t *testing.T) {
	srv, got := stubSeq(t, reply{body: providersPOI}, createdEstimate)

	out, errb, err := run(t, estimateCreateCommand(), srv, poiFlags...)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 2 || got.paths[0] != "/api/v2"+providersPath || got.paths[1] != estimateCreatePath {
		t.Fatalf("paths = %v, want the providers catalog then %s", got.paths, estimateCreatePath)
	}

	var sent map[string]any
	if err := json.Unmarshal([]byte(got.bodies[1]), &sent); err != nil {
		t.Fatalf("estimate body is not JSON: %v\n%s", err, got.bodies[1])
	}
	ds := firstDataset(t, sent)
	if sent["name"] != "SF visitors" || ds["type"] != "POI" || ds["start_date"] != "2026-09-02" {
		t.Errorf("body = %s", got.bodies[1])
	}
	if raw, _ := json.Marshal(ds["signal_providers"]); string(raw) != `["aaa","bbb"]` {
		t.Errorf("signal_providers = %s, want every provider, as create sends", raw)
	}
	// The same command line, as a create, builds the very same body.
	createSrv, _ := stub(t, providersPOI)
	if want := dryRunBody(t, audiencesCreateCommand(), createSrv, poiFlags...); !reflect.DeepEqual(sent, want) {
		t.Errorf("estimate body differs from the create body:\n estimate %v\n create   %v", sent, want)
	}

	// The route replays a retried estimate rather than scanning twice.
	if len(got.keys[1]) != 36 {
		t.Errorf("Idempotency-Key = %q, want a UUID", got.keys[1])
	}
	if !strings.Contains(out, "pending") || strings.Contains(out, "uniques") {
		t.Errorf("stdout should be the queued estimate:\n%s", out)
	}
	if !strings.Contains(errb, "'intuizi audiences estimate show <id> --wait'") {
		t.Errorf("stderr should say how to follow the estimate:\n%s", errb)
	}
}

// --file is forwarded untouched, a field no flag writes included.
func TestEstimateCreateForwardsTheFile(t *testing.T) {
	srv, got := stubSeq(t, createdEstimate)
	payload := `{"name":"Two sets","operator":"AND","datasets":[{"type":"POI"},{"type":"Apps"}],"analyses":{"frequency":true}}`

	if _, _, err := run(t, estimateCreateCommand(), srv, "--file", payloadFile(t, payload)); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(got.paths) != 1 || got.paths[0] != estimateCreatePath {
		t.Fatalf("paths = %v", got.paths)
	}
	if got.bodies[0] != payload {
		t.Errorf("body = %s, want the file as written", got.bodies[0])
	}
}

func TestEstimateCreateDryRunSendsNothing(t *testing.T) {
	srv, got := stub(t, providersPOI)

	body := dryRunBody(t, estimateCreateCommand(), srv, poiFlags...)

	if body["name"] != "SF visitors" {
		t.Errorf("body = %v", body)
	}
	for _, p := range got.paths {
		if p == estimateCreatePath {
			t.Errorf("--dry-run sent the estimate: %v", got.paths)
		}
	}
}

func TestEstimateCreateWaitFollowsToCompleted(t *testing.T) {
	fast(t)
	srv, got := stubSeq(t, createdEstimate,
		estimate("processing", "null", ""), estimate("completed", completedNumbers, ""))

	out, errb, err := run(t, estimateCreateCommand(), srv,
		"--file", payloadFile(t, `{"name":"x","datasets":[{"type":"POI"}]}`), "--wait")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := []string{estimateCreatePath, estimatePath12, estimatePath12}
	if strings.Join(got.paths, " ") != strings.Join(want, " ") {
		t.Errorf("paths = %v, want %v", got.paths, want)
	}
	for _, line := range []string{"created estimate 12", "estimate 12: processing", "estimate 12: completed"} {
		if !strings.Contains(errb, line) {
			t.Errorf("stderr missing %q:\n%s", line, errb)
		}
	}
	if !strings.Contains(out, "482311") {
		t.Errorf("stdout should be the completed estimate:\n%s", out)
	}
}

// normalized_payload carries a name of its own, and the {id, name} collapse
// made the whole payload read as that name. It is summarised like the other
// nested objects instead.
func TestEstimateShowSummarisesTheNormalizedPayload(t *testing.T) {
	srv, _ := stubSeq(t, estimate("completed", completedNumbers, ""))

	out, _, err := run(t, estimateShowCommand(), srv, "12")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !containsRow(out, "normalized_payload", "{...}") {
		t.Errorf("normalized_payload should read {...}:\n%s", out)
	}
}

// A wait that gives up names the same resume command a timeout does.
func TestEstimateWaitGiveUpNamesTheResumeCommand(t *testing.T) {
	fast(t)
	down := reply{status: 500, body: errEnvelope}
	srv, _ := stubSeq(t, estimate("processing", "null", ""), down, down, down)

	_, _, err := run(t, estimateShowCommand(), srv, "12", "--wait", "--timeout", "5s")
	if !errors.Is(err, errWaitGaveUp) {
		t.Fatalf("err = %v, want errWaitGaveUp", err)
	}
	if !strings.Contains(err.Error(), "'intuizi audiences estimate show 12 --wait'") {
		t.Errorf("error should name the resume command: %v", err)
	}
}
