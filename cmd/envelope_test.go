package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// Every command meets the same three server responses: a success envelope, a
// generic error envelope, and a 422 validation error. Before this table only
// the auth token mint had ever been shown a 422, so the field-error rendering
// and the exit code that goes with it were untested everywhere else.
//
// The error bodies stay inline rather than in testdata: they are three lines,
// and the assertions below read these exact strings back.
const (
	errEnvelope = `{"status":"error","code":500,"message":"Server error.","data":[]}`

	// Validation errors nest under data, not at the top level.
	valEnvelope = `{"status":"error","code":422,"message":"Validation error.",` +
		`"data":{"errors":{"name":["The name field is required."]}}}`
)

// One list body serves every index command: it carries the union of the
// columns the families render, and summarise leaves an empty cell for a column
// the response omits.
const listEnvelope = `{"status":"success","code":200,"data":{"items":[
 {"id":1,"name":"Example","status":{"id":104,"name":"Completed"},
  "results_count":10,"total_eids":10,"description":"Example export",
  "audience":{"id":2,"name":"Example audience"},
  "created_at":"2026-01-01 00:00:00"}],
 "pagination":{"current_page":1,"per_page":25,"total":1,"last_page":1}}}`

const singleEnvelope = `{"status":"success","code":200,"data":[
 {"id":4,"name":"Retail 2026","created_at":"2026-01-01 00:00:00"}]}`

const createdEnvelope = `{"status":"success","code":201,"data":[
 {"id":4,"name":"Retail 2026"}]}`

const usageEnvelope = `{"status":"success","code":200,"data":[
 {"yearmonth":"2026-06","data_scanned":{"bytes":1,"formatted":"1 B"},
  "operations":[{"operation_type":"audience_build","label":"Audience Build",
   "bytes":1,"formatted":"1 B"}],
  "limit":{"data_scan_limit_formatted":"10 TB","percent_used":0,
   "over_limit":false,"enforced":true}}]}`

// The POI submission index is not paginated: its data is a flat array, which
// ReadList takes as a second shape.
const poiListEnvelope = `{"status":"success","code":200,"data":[
 {"id":1,"name":"Example POI","status":"waiting","created_at":"2026-01-01 00:00:00"}]}`

// The commands whose payload is too nested for flags read it from a file. The
// bytes are passed through untouched, so any JSON object will do.
const createPayload = `{"name":"Example audience","operator":"Single","datasets":[]}`

// postID commands answer with an empty data array and say what happened on
// stderr, keeping stdout clean for a pipe.
const emptyEnvelope = `{"status":"success","code":200,"data":[]}`

// payloadFile writes a create body to a temp file, for the --file commands.
func payloadFile(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// envelopeCase is one command, run against all three responses.
//
// Success is asserted on the columns and labels the command renders, never on
// a fixture's values. That is what keeps the captured fixtures re-capturable:
// a refreshed audience-completed.json changes ids and counts, and no assertion
// here notices.
type envelopeCase struct {
	name    string
	cmd     func() *cobra.Command
	args    []string
	path    string
	body    string // inline success envelope; empty means read the fixture
	fixture string
	head    string   // exact header line, for the table commands
	labels  []string // key labels, for the key/value commands

	// payload, when set, is written to a temp file and passed as --file.
	payload string
	// errLabels are the success commentary on stderr, for the postID
	// commands, which print nothing to stdout at all.
	errLabels []string
}

// caseArgs resolves a case's arguments, materialising its --file payload.
func caseArgs(t *testing.T, c envelopeCase) []string {
	t.Helper()

	args := append([]string(nil), c.args...)
	if c.payload != "" {
		args = append(args, "--file", payloadFile(t, c.payload))
	}
	return args
}

func envelopeCases() []envelopeCase {
	return []envelopeCase{
		{
			name: "audiences list", cmd: audiencesListCommand,
			path: "/api/v2/analyses/audiences/index", body: listEnvelope,
			head: "id name status results_count created_at",
		},
		{
			name: "activations list", cmd: activationsListCommand,
			path: "/api/v2/analyses/activations/index", body: listEnvelope,
			head: "id description status audience created_at",
		},
		{
			name: "cohorts list", cmd: cohortsListCommand,
			path: "/api/v2/analyses/cohorts/index", body: listEnvelope,
			head: "id name status total_eids created_at",
		},
		{
			name: "projects list", cmd: projectsListCommand,
			path: "/api/v2/analyses/projects/index", body: listEnvelope,
			head: "id name",
		},
		{
			name: "schedules list", cmd: schedulesListCommand,
			path: "/api/v2/analyses/schedules/index", body: listEnvelope,
			head: "id name status",
		},
		{
			name: "audiences show", cmd: audiencesShowCommand,
			args: []string{"15572"}, path: "/api/v2/analyses/audiences/15572",
			fixture: "audience-completed.json",
			labels:  []string{"results_count", "is_activation_allowed", "eligibility"},
		},
		{
			name: "activations show", cmd: activationsShowCommand,
			args: []string{"5621"}, path: "/api/v2/analyses/activations/5621",
			fixture: "activation-completed.json",
			labels:  []string{"description", "pricing_model", "datastreams"},
		},
		{
			name: "projects show",
			cmd:  func() *cobra.Command { return showCommand("project", projectsPrefix, projectColumns) },
			args: []string{"4"}, path: "/api/v2/analyses/projects/4",
			body: singleEnvelope, labels: []string{"name", "created_at"},
		},
		{
			name: "projects create", cmd: projectsCreateCommand,
			args: []string{"--name", "Retail 2026"},
			path: "/api/v2/analyses/projects/create", body: createdEnvelope,
			labels: []string{"name"},
		},
		{
			name: "usage", cmd: usageCommand,
			args: []string{"--month", "2026-06"}, path: "/api/v2/usage",
			body: usageEnvelope, labels: []string{"2026-06", "Audience Build"},
		},

		// --- createFromFile: the file-payload creates. A 422 here is the
		// most likely failure a user meets, since it is what a bad audience
		// or cohort body returns.
		{
			name: "audiences create", cmd: audiencesCreateCommand,
			payload: createPayload, path: "/api/v2/analyses/audiences/create",
			body: createdEnvelope, labels: []string{"name"},
		},
		{
			name: "audiences lookalike create", cmd: audiencesLookalikeCommand,
			args: []string{"create"}, payload: createPayload,
			path: "/api/v2/analyses/audiences/create-lookalike",
			body: createdEnvelope, labels: []string{"name"},
		},
		{
			name: "cohorts create", cmd: cohortsCreateCommand,
			payload: createPayload, path: "/api/v2/analyses/cohorts/create",
			body: createdEnvelope, labels: []string{"name"},
		},
		{
			name: "cohorts preview", cmd: cohortsPreviewCommand,
			payload: createPayload, path: "/api/v2/analyses/cohorts/preview",
			body: singleEnvelope, labels: []string{"name"},
		},
		{
			name: "schedules create", cmd: schedulesCreateCommand,
			payload: createPayload, path: "/api/v2/analyses/schedules/create",
			body: createdEnvelope, labels: []string{"name"},
		},

		// --- postID: delete and the schedule toggles. Nothing on stdout,
		// commentary on stderr.
		{
			name: "projects delete",
			cmd:  func() *cobra.Command { return deleteCommand("project", projectsPrefix) },
			args: []string{"4", "--yes"},
			path: "/api/v2/analyses/projects/delete-by-id",
			body: emptyEnvelope, errLabels: []string{"deleted project 4"},
		},
		{
			name: "schedules activate",
			cmd:  func() *cobra.Command { return toggleCommand("activate", "activate", "activated", "") },
			args: []string{"7"}, path: "/api/v2/analyses/schedules/activate",
			body: emptyEnvelope, errLabels: []string{"activated schedule 7"},
		},

		// --- POI, whose own tests walk every route but never a failure.
		{
			name: "poi submissions list", cmd: poiSubmissionsCommand,
			args: []string{"list"},
			path: "/api/v2/my-data/pois/submissions/index",
			body: poiListEnvelope, head: "id name status created_at",
		},
	}
}

// The reference reads share one generic runner, so they are pinned by
// TestReferenceReadHandlesErrorEnvelopes below rather than by a row each.

func TestEnvelopeMatrixSuccess(t *testing.T) {
	for _, c := range envelopeCases() {
		t.Run(c.name, func(t *testing.T) {
			body := c.body
			if body == "" {
				body = fixture(t, c.fixture)
			}

			srv, got := stubSeq(t, reply{body: body})
			out, errb, err := run(t, c.cmd(), srv, caseArgs(t, c)...)
			if err != nil {
				t.Fatalf("execute: %v", err)
			}

			if len(got.paths) == 0 {
				t.Fatal("no request was made")
			}
			if got.paths[0] != c.path {
				t.Errorf("path = %s, want %s", got.paths[0], c.path)
			}

			if c.head != "" {
				head := strings.Join(strings.Fields(strings.SplitN(out, "\n", 2)[0]), " ")
				if head != c.head {
					t.Errorf("columns = %q, want %q", head, c.head)
				}
			}
			for _, label := range c.labels {
				if !strings.Contains(out, label) {
					t.Errorf("output missing %q:\n%s", label, out)
				}
			}
			for _, label := range c.errLabels {
				if !strings.Contains(errb, label) {
					t.Errorf("stderr missing %q:\n%s", label, errb)
				}
				// These commands report on stderr only.
				if out != "" {
					t.Errorf("stdout should stay clean, got %q", out)
				}
			}
		})
	}
}

// An error envelope must reach the user as the server's own message, and exit
// 1: a failed call is not a misuse of the CLI.
func TestEnvelopeMatrixErrorEnvelope(t *testing.T) {
	for _, c := range envelopeCases() {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := stubSeq(t, reply{status: 500, body: errEnvelope})
			out, _, err := run(t, c.cmd(), srv, caseArgs(t, c)...)

			if err == nil {
				t.Fatal("expected an error from a 500 envelope")
			}
			if code := exitCode(err, nil, true); code != exitError {
				t.Errorf("exit code = %d, want %d", code, exitError)
			}
			if !strings.Contains(err.Error(), "Server error. (500)") {
				t.Errorf("error = %v, want the envelope's own message", err)
			}
			// A pipe must not see half a table when the call failed.
			if out != "" {
				t.Errorf("failed command wrote to stdout: %q", out)
			}
		})
	}
}

// A 422 reads like a usage mistake but is not one: the CLI sent a
// well-formed request and the server rejected its contents, so it exits 1,
// not 2. The field errors render sorted by key, which is why they are safe to
// assert on at all.
func TestEnvelopeMatrix422(t *testing.T) {
	for _, c := range envelopeCases() {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := stubSeq(t, reply{status: 422, body: valEnvelope})
			out, _, err := run(t, c.cmd(), srv, caseArgs(t, c)...)

			if err == nil {
				t.Fatal("expected an error from a 422 envelope")
			}
			if code := exitCode(err, nil, true); code != exitError {
				t.Errorf("exit code = %d, want %d (a 422 is an API error, not a misuse)",
					code, exitError)
			}
			if !strings.Contains(err.Error(), "(422)") {
				t.Errorf("error = %v, want the status code", err)
			}
			if !strings.Contains(err.Error(), "name: The name field is required.") {
				t.Errorf("error = %v, want the nested field errors", err)
			}
			if out != "" {
				t.Errorf("failed command wrote to stdout: %q", out)
			}
		})
	}
}

// Every reference read is built from the same table and runs through one
// generic runner, so a single endpoint standing in for all of them is enough
// to pin what a failure looks like.
func TestReferenceReadHandlesErrorEnvelopes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"error envelope", 500, errEnvelope, "Server error. (500)"},
		{"validation error", 422, valEnvelope, "name: The name field is required."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := findEndpoint(t, "common", "countries")
			srv, _ := stubSeq(t, reply{status: tc.status, body: tc.body})

			out, _, err := run(t, e.command("common"), srv)
			if err == nil {
				t.Fatal("expected an error")
			}
			if code := exitCode(err, nil, true); code != exitError {
				t.Errorf("exit code = %d, want 1", code)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want %q", err, tc.want)
			}
			if out != "" {
				t.Errorf("failed read wrote to stdout: %q", out)
			}
		})
	}
}
