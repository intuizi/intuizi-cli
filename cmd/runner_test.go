package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Commands are built fresh from their constructors rather than reached through
// rootCmd: the tree is assembled in init(), so a flag set by one test would
// still be set in the next.

type capture struct {
	paths   []string
	queries []string
	bodies  []string
	keys    []string
}

// stub serves one canned body and records what was asked for.
func stub(t *testing.T, body string) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.paths = append(got.paths, r.URL.Path)
		got.queries = append(got.queries, r.URL.RawQuery)
		got.keys = append(got.keys, r.Header.Get("Idempotency-Key"))
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			got.bodies = append(got.bodies, string(b))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

// run executes one command against the stub, returning stdout and stderr.
// jsonOutput is forced off so a leaked --json from another test cannot change
// what this one sees; runJSON is the opt-in for testing the --json path.
func run(t *testing.T, cmd *cobra.Command, srv *httptest.Server, args ...string) (string, string, error) {
	return exec(t, false, cmd, srv, args...)
}

func runJSON(t *testing.T, cmd *cobra.Command, srv *httptest.Server, args ...string) (string, string, error) {
	return exec(t, true, cmd, srv, args...)
}

func exec(t *testing.T, asJSON bool, cmd *cobra.Command, srv *httptest.Server, args ...string) (string, string, error) {
	t.Helper()
	t.Setenv("INTUIZI_API_TOKEN", "tok")

	prevBase, prevJSON := baseURLFlag, jsonOutput
	baseURLFlag, jsonOutput = srv.URL, asJSON
	t.Cleanup(func() { baseURLFlag, jsonOutput = prevBase, prevJSON })

	// rootCmd silences usage on error for the whole tree; a command built from
	// its constructor has no root, so mirror that here or every error-path test
	// sees the usage text on stdout.
	cmd.SilenceUsage = true

	var out, errb bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errb)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), errb.String(), err
}

const audienceList = `{"status":"success","code":200,"data":{"items":[
 {"id":88,"name":"Coffee Buyers NYC","status":{"id":104,"name":"Completed"},
  "results_count":482311,"operator":"AND","project":{"id":4,"name":"Retail 2025"},
  "created_at":"2025-01-01 12:00:00"},
 {"id":91,"name":"Gym\tvisitors","status":{"id":100,"name":"Initiating"},
  "results_count":0,"created_at":"2025-02-03 08:15:00"}],
 "pagination":{"current_page":1,"per_page":25,"total":57,"last_page":3}}}`

func TestListRendersSummaryColumns(t *testing.T) {
	srv, got := stub(t, audienceList)

	out, errb, err := run(t, audiencesListCommand(), srv, "--search", "co ffee", "--page", "2")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got.paths[0] != "/api/v2/analyses/audiences/index" {
		t.Errorf("path = %s", got.paths[0])
	}
	// An unset --per-page must not become per_page=0, which the API rejects.
	if strings.Contains(got.queries[0], "per_page") {
		t.Errorf("unset --per-page leaked: %s", got.queries[0])
	}
	if !strings.Contains(got.queries[0], "search=co+ffee") || !strings.Contains(got.queries[0], "page=2") {
		t.Errorf("query = %s", got.queries[0])
	}

	head := strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " ")
	if head != "id name status results_count created_at" {
		t.Errorf("columns = %q", head)
	}
	if !strings.Contains(out, "Completed") || strings.Contains(out, "{...}") {
		t.Errorf("status not flattened:\n%s", out)
	}
	if strings.Contains(out, "Retail 2025") || strings.Contains(out, "AND") {
		t.Errorf("unrequested columns leaked:\n%s", out)
	}
	// An embedded tab would break tabwriter alignment.
	if !strings.Contains(out, "Gym visitors") {
		t.Errorf("tab not flattened:\n%s", out)
	}
	if !strings.Contains(errb, "page 1 of 3, 57 total") {
		t.Errorf("footer = %q", errb)
	}
}

func TestListEmptySaysSoOnStderr(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"data":{"items":[],"pagination":{"current_page":1,"per_page":25,"total":0,"last_page":1}}}`)

	out, errb, err := run(t, cohortsListCommand(), srv)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out != "" {
		t.Errorf("stdout should stay clean for a pipe, got %q", out)
	}
	if !strings.Contains(errb, "no cohorts") {
		t.Errorf("stderr = %q", errb)
	}
}

// The single-resource read returns data as a one-element array, not an object.
func TestShowUnwrapsOneElementArray(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[
	 {"id":88,"name":"Coffee Buyers NYC","status":{"id":104,"name":"Completed"},
	  "results_count":482311,"is_activation_allowed":true}]}`)

	out, _, err := run(t, showCommand("audience", audiencesPrefix, audienceColumns), srv, "88")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/analyses/audiences/88" {
		t.Errorf("path = %s", got.paths[0])
	}
	for _, want := range []string{"Coffee Buyers NYC", "Completed", "482311", "is_activation_allowed"} {
		if !strings.Contains(out, want) {
			t.Errorf("show output missing %q:\n%s", want, out)
		}
	}
	// Lead fields come first, the rest alphabetically after.
	if !strings.HasPrefix(out, "id") {
		t.Errorf("id should lead:\n%s", out)
	}
}

func TestShowRejectsABadID(t *testing.T) {
	srv, got := stub(t, `{}`)
	if _, _, err := run(t, showCommand("audience", audiencesPrefix, audienceColumns), srv, "eighty-eight"); err == nil {
		t.Fatal("expected an error for a non-numeric id")
	}
	if len(got.paths) != 0 {
		t.Errorf("a bad id should cost no round trip, got %v", got.paths)
	}
}

const created = `{"status":"success","code":201,"data":[
 {"id":88,"name":"Coffee visitors","status":{"id":100,"name":"Initiating"},"results_count":0}]}`

func TestCreateFromFileSendsBodyVerbatimWithIdempotencyKey(t *testing.T) {
	srv, got := stub(t, created)

	dir := t.TempDir()
	path := filepath.Join(dir, "audience.json")
	payload := `{"name":"Coffee visitors","datasets":[{"type":"POI","unknown_future_field":true}]}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}

	out, errb, err := run(t, audiencesCreateCommand(), srv, "--file", path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Passed through untouched, so a field the CLI has never heard of survives.
	if !strings.Contains(got.bodies[0], "unknown_future_field") {
		t.Errorf("body was not passed through verbatim: %s", got.bodies[0])
	}
	if got.keys[0] == "" {
		t.Error("create sent no Idempotency-Key")
	}
	if !strings.Contains(out, "Initiating") {
		t.Errorf("stdout = %q", out)
	}
	if !strings.Contains(errb, "still building") {
		t.Errorf("stderr should point at show, got %q", errb)
	}
}

func TestCreateReadsStdinForDashFile(t *testing.T) {
	srv, got := stub(t, created)

	cmd := audiencesCreateCommand()
	cmd.SetIn(strings.NewReader(`{"name":"From a pipe"}`))
	if _, _, err := run(t, cmd, srv, "--file", "-"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(got.bodies[0], "From a pipe") {
		t.Errorf("stdin body = %s", got.bodies[0])
	}
}

func TestCreateRejectsBadJSONWithoutARoundTrip(t *testing.T) {
	srv, got := stub(t, created)

	dir := t.TempDir()
	path := filepath.Join(dir, "broken.json")
	if err := os.WriteFile(path, []byte(`{"name": `), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, audiencesCreateCommand(), srv, "--file", path)
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "broken.json") {
		t.Errorf("the error should name the file, got %v", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("invalid JSON should cost no round trip, got %v", got.paths)
	}
}

func TestDeleteAbortsOnNo(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	cmd := deleteCommand("audience", audiencesPrefix)
	cmd.SetIn(strings.NewReader("n\n"))
	_, errb, err := run(t, cmd, srv, "88")

	if err == nil || err.Error() != "aborted" {
		t.Fatalf("err = %v, want aborted", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("an aborted delete must send nothing, got %v", got.paths)
	}
	if !strings.Contains(errb, "Delete audience 88?") {
		t.Errorf("prompt = %q", errb)
	}
}

func TestDeleteWithYesPostsTheID(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	out, errb, err := run(t, deleteCommand("audience", audiencesPrefix), srv, "88", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/analyses/audiences/delete-by-id" {
		t.Errorf("path = %s", got.paths[0])
	}

	var body map[string]int
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatalf("body %q: %v", got.bodies[0], err)
	}
	if body["id"] != 88 {
		t.Errorf("body = %v", body)
	}
	// delete-by-id is not one of the idempotency-keyed create routes.
	if got.keys[0] != "" {
		t.Errorf("delete should carry no Idempotency-Key, got %q", got.keys[0])
	}
	if out != "" {
		t.Errorf("stdout should stay clean, got %q", out)
	}
	if !strings.Contains(errb, "deleted audience 88") {
		t.Errorf("stderr = %q", errb)
	}
}

func TestDeleteYesDoesNotLeakBetweenCommands(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"data":[]}`)

	if _, _, err := run(t, deleteCommand("audience", audiencesPrefix), srv, "88", "--yes"); err != nil {
		t.Fatalf("first delete: %v", err)
	}

	// A fresh command must ask again rather than inherit the previous --yes.
	cmd := deleteCommand("audience", audiencesPrefix)
	cmd.SetIn(strings.NewReader("n\n"))
	if _, _, err := run(t, cmd, srv, "91"); err == nil {
		t.Fatal("second delete should have prompted and aborted")
	}
}

func TestActivationsCreateBuildsBodyFromFlags(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":201,"data":[{"id":501,"description":"Q1","status":{"id":100,"name":"Initiating"}}]}`)

	_, _, err := run(t, activationsCreateCommand(), srv,
		"--audience-id", "88", "--endpoint-connection-id", "12", "--pricing-model-id", "3")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]float64{
		"audience_id": 88, "endpoint_connection_id": 12, "pricing_model_id": 3,
	} {
		if body[k] != want {
			t.Errorf("%s = %v, want %v", k, body[k], want)
		}
	}
	// Unset optional flags must not go out as zero values.
	if _, ok := body["project_id"]; ok {
		t.Errorf("unset --project-id leaked: %v", body)
	}
	if _, ok := body["description"]; ok {
		t.Errorf("unset --description leaked: %v", body)
	}
}

func TestActivationsCreateNeedsFileOrAllThreeIDs(t *testing.T) {
	srv, got := stub(t, `{}`)

	if _, _, err := run(t, activationsCreateCommand(), srv, "--audience-id", "88"); err == nil {
		t.Fatal("expected an error when only one id is given")
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestActivationsCreateRejectsFileMixedWithFlags(t *testing.T) {
	srv, _ := stub(t, `{}`)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte(`{"audience_id":88}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, activationsCreateCommand(), srv, "--file", path, "--audience-id", "88")
	if err == nil || !strings.Contains(err.Error(), "--file carries the whole body") {
		t.Fatalf("err = %v", err)
	}
}

func TestProjectsCreateSendsName(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":201,"data":[{"id":4,"name":"Retail 2026"}]}`)

	if _, _, err := run(t, projectsCreateCommand(), srv, "--name", "Retail 2026"); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(got.bodies[0], `"name":"Retail 2026"`) {
		t.Errorf("body = %s", got.bodies[0])
	}
	if got.keys[0] == "" {
		t.Error("projects create sent no Idempotency-Key")
	}
}

func TestScheduleToggleHitsItsOwnRoute(t *testing.T) {
	for _, tc := range []struct{ verb, path string }{
		{"activate", "/api/v2/analyses/schedules/activate"},
		{"deactivate", "/api/v2/analyses/schedules/deactivate"},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)
			cmd := toggleCommand(tc.verb, tc.verb, tc.verb+"d", "")
			if _, _, err := run(t, cmd, srv, "42"); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got.paths[0] != tc.path {
				t.Errorf("path = %s, want %s", got.paths[0], tc.path)
			}
		})
	}
}

func TestSubmissionsListTranslatesFlagsToAPISpelling(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[{"id":1,"name":"Hilton","status":"waiting"}]}`)

	// Executed through the parent: cobra's Execute walks up to the tree root,
	// so running the subcommand object directly would run "submissions".
	subs := poiSubmissionsCommand()
	if _, _, err := run(t, subs, srv,
		"list", "--search", "Hilton", "--sort-by", "status", "--order", "desc"); err != nil {
		t.Fatalf("execute: %v", err)
	}

	q := got.queries[0]
	for _, want := range []string{"q=Hilton", "sortBy=status", "orderBy=desc"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
}

func TestSubmissionsCreateNeedsExactlyOneSource(t *testing.T) {
	srv, got := stub(t, `{}`)

	if _, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "x", "--brand-id", "1"); err == nil {
		t.Fatal("expected an error with no source")
	}
	if _, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "x", "--brand-id", "1", "--list", "a.json", "--upload-reference", "upl_1"); err == nil {
		t.Fatal("expected an error with two sources")
	}
	if len(got.paths) != 0 {
		t.Errorf("neither should reach the API, got %v", got.paths)
	}
}

func TestUsageRejectsABadMonthOffline(t *testing.T) {
	srv, got := stub(t, `{}`)

	if _, _, err := run(t, usageCommand(), srv, "--month", "June"); err == nil {
		t.Fatal("expected an error for a non YYYY-MM month")
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

func TestUsageRendersBreakdown(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"data":[{
	 "yearmonth":"2026-06",
	 "data_scanned":{"bytes":5497558138880,"formatted":"5 TB"},
	 "operations":[{"operation_type":"audience_build","label":"Audience Build","bytes":1,"formatted":"1 B"},
	               {"operation_type":"activation","label":"Activation","bytes":0,"formatted":"0 B"}],
	 "limit":{"data_scan_limit_formatted":"10 TB","percent_used":54.9,"over_limit":false,"enforced":true}}]}`)

	out, _, err := run(t, usageCommand(), srv, "--month", "2026-06")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{"2026-06", "5 TB", "10 TB", "54.9", "audience_build", "Activation"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output missing %q:\n%s", want, out)
		}
	}
}

// --json must print the envelope the server sent, on writes as well as reads.
// A create that printed a bare data array while list printed a full envelope
// would force a script to parse the two differently.
func TestCreateJSONPrintsTheEnvelopeNotJustData(t *testing.T) {
	srv, _ := stub(t, created)

	dir := t.TempDir()
	path := filepath.Join(dir, "a.json")
	if err := os.WriteFile(path, []byte(`{"name":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, err := runJSON(t, audiencesCreateCommand(), srv, "--file", path)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	for _, want := range []string{`"status"`, `"code"`, `"data"`} {
		if !strings.Contains(out, want) {
			t.Errorf("create --json output is missing %s:\n%s", want, out)
		}
	}
}

func TestDeleteJSONPrintsTheEnvelopeNotJustData(t *testing.T) {
	srv, _ := stub(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	out, _, err := runJSON(t, deleteCommand("audience", audiencesPrefix), srv, "88", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(out, `"status"`) || !strings.Contains(out, `"code"`) {
		t.Errorf("delete --json output is not an envelope:\n%s", out)
	}
}
