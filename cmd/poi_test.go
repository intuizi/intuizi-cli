package cmd

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// formFields parses a captured multipart body back into name -> value. The
// boundary is not recorded alongside the request, but it is the body's own
// first line, so it can be read back off the wire format.
func formFields(t *testing.T, body string) map[string]string {
	t.Helper()
	first, _, ok := strings.Cut(body, "\r\n")
	if !ok || !strings.HasPrefix(first, "--") {
		t.Fatalf("not a multipart body: %.60q", body)
	}
	r := multipart.NewReader(strings.NewReader(body), strings.TrimPrefix(first, "--"))
	out := map[string]string{}
	for {
		part, err := r.NextPart()
		if err != nil {
			return out
		}
		b, _ := io.ReadAll(part)
		out[part.FormName()] = string(b)
	}
}

func csvFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "locations.csv")
	body := "name,latitude,longitude,country|alpha_2\n" +
		"example-coffee-01,39.7817,-89.6501,US\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A create returns {value,text}; only a read returns {id,name,status,...}.
// Captured from staging.
const submissionEnvelope = `{"status":"success","code":201,` +
	`"message":"Resource created successfully.",` +
	`"data":[{"value":31,"text":"cli-task-7-test"}]}`

// --------------------------------------------------------------------------------- submissions: source choice

func TestPoiSubmissionsCreateNeedsExactlyOneSource(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no source", []string{"--name", "x", "--brand-id", "9"}},
		{"file and list", []string{"--name", "x", "--brand-id", "9", "--file", "a.csv", "--list", "b.json"}},
		{"file and upload", []string{"--name", "x", "--brand-id", "9", "--file", "a.csv", "--upload-reference", "upl_1"}},
		{"all three", []string{"--file", "a.csv", "--list", "b.json", "--upload-reference", "upl_1"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{}`)

			_, _, err := run(t, poiSubmissionCreateCommand(), srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), "exactly one of") {
				t.Fatalf("err = %v", err)
			}
			if len(got.paths) != 0 {
				t.Errorf("a bad source choice should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// --update and --remove say what to do with an existing POI; --key says how to
// recognise it. One without the other is meaningless, so it is caught locally,
// on every source route.
func TestPoiSubmissionsCreateMatchFlagsNeedAKey(t *testing.T) {
	sources := map[string][]string{
		"file":   {"--name", "x", "--brand-id", "9", "--file", "a.csv"},
		"upload": {"--name", "x", "--brand-id", "9", "--upload-reference", "upl_1"},
		"list":   {"--list", "locs.json"},
	}
	for source, base := range sources {
		for _, flag := range []string{"--update", "--remove"} {
			t.Run(source+" "+flag, func(t *testing.T) {
				srv, got := stub(t, `{}`)

				_, _, err := run(t, poiSubmissionCreateCommand(), srv, append(base, flag)...)
				if err == nil || !strings.Contains(err.Error(), "--key") {
					t.Fatalf("err = %v", err)
				}
				if len(got.paths) != 0 {
					t.Errorf("should cost no round trip, got %v", got.paths)
				}
			})
		}
	}
}

// --key is a closed set the server validates; a typo should fail here, before
// it costs a round trip and a 422.
func TestPoiSubmissionsCreateRejectsAnUnknownKey(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "x", "--brand-id", "9", "--upload-reference", "upl_1",
		"--update", "--key", "storeid")
	if err == nil || !strings.Contains(err.Error(), "--key must be") ||
		!strings.Contains(err.Error(), "store-id") {
		t.Fatalf("err = %v, want one listing the accepted keys", err)
	}
	if exitCode(err, nil, true) != 2 {
		t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// --list reads stdin as "-"; --file does not, and the raw "open -: no such
// file" it used to surface said nothing about the route that does.
func TestPoiSubmissionsCreateFileRejectsStdin(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "x", "--brand-id", "9", "--file", "-")
	if err == nil || !strings.Contains(err.Error(), "--list -") {
		t.Fatalf("err = %v, want one pointing at --list -", err)
	}
	if exitCode(err, nil, true) != 2 {
		t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// The API accepts .csv and .txt; anything else is a 422 after a 50 MB upload,
// so the extension is checked before the file is opened.
func TestPoiSubmissionsCreateFileChecksTheExtension(t *testing.T) {
	for _, bad := range []string{"locations.json", "locations.xlsx", "locations"} {
		t.Run(bad, func(t *testing.T) {
			srv, got := stub(t, `{}`)

			_, _, err := run(t, poiSubmissionCreateCommand(), srv,
				"--name", "x", "--brand-id", "9", "--file", bad)
			if err == nil || !strings.Contains(err.Error(), ".csv or .txt") {
				t.Fatalf("err = %v, want one naming the accepted extensions", err)
			}
			if exitCode(err, nil, true) != 2 {
				t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}

	// Case does not matter, and .txt is as welcome as .csv.
	for _, ok := range []string{"LOCATIONS.CSV", "locations.txt"} {
		t.Run(ok, func(t *testing.T) {
			srv, got := stub(t, submissionEnvelope)
			path := filepath.Join(t.TempDir(), ok)
			if err := os.WriteFile(path, []byte("name,latitude,longitude\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := run(t, poiSubmissionCreateCommand(), srv,
				"--name", "x", "--brand-id", "9", "--file", path); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if len(got.paths) != 1 {
				t.Errorf("paths = %v", got.paths)
			}
		})
	}
}

// --------------------------------------------------------------------------------- submissions: create-by-file

// The encoding the API is strict about: create-by-file is multipart, and its
// boolean form fields must arrive as "1" - the string "true" is rejected.
func TestPoiSubmissionsCreateByFileSendsOnesNotTrue(t *testing.T) {
	srv, got := stub(t, submissionEnvelope)

	_, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "cli-task-7-test",
		"--brand-id", "9",
		"--file", csvFile(t),
		"--update",
		"--key", "gps-coordinates")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	if got.paths[0] != "/api/v2/my-data/pois/submissions/create-by-file" {
		t.Errorf("path = %s", got.paths[0])
	}

	fields := formFields(t, got.bodies[0])
	for field, want := range map[string]string{
		"name":     "cli-task-7-test",
		"brand_id": "9",
		"update":   "1",
		"key":      "gps-coordinates",
	} {
		if fields[field] != want {
			t.Errorf("%s = %q, want %q", field, fields[field], want)
		}
	}
	// The country header carries the isocode form, so the CSV must travel byte
	// for byte - a rewritten header is rejected on ingestion.
	if !strings.Contains(fields["locations_file"], "country|alpha_2") {
		t.Errorf("the CSV did not travel intact: %q", fields["locations_file"])
	}
	// An unset boolean is absent, not "0" - the API treats absent as no.
	if _, ok := fields["remove"]; ok {
		t.Errorf("unset --remove leaked: %v", fields)
	}
}

// --------------------------------------------------------------------------------- submissions: create-by-upload

// create-by-upload is JSON, and its contract declares update/remove as real
// booleans. The "1"/"0" rule belongs to the multipart route alone, so the two
// encodings are meant to differ.
func TestPoiSubmissionsCreateByUploadSendsJSONBooleans(t *testing.T) {
	srv, got := stub(t, submissionEnvelope)

	_, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--name", "cli-task-7-test",
		"--brand-id", "9",
		"--upload-reference", "upl_abc123",
		"--remove",
		"--key", "external-id")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/my-data/pois/submissions/create-by-upload" {
		t.Errorf("path = %s", got.paths[0])
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["remove"] != true {
		t.Errorf("remove = %#v, want the JSON boolean true", body["remove"])
	}
	if body["upload_reference"] != "upl_abc123" || body["key"] != "external-id" {
		t.Errorf("body = %v", body)
	}
	if _, ok := body["update"]; ok {
		t.Errorf("unset --update leaked: %v", body)
	}
}

// --------------------------------------------------------------------------------- submissions: create-by-list

func TestMergeSubmissionFields(t *testing.T) {
	tests := []struct {
		name     string
		payload  string
		setName  bool
		setBrand bool
		wantErr  string
		wantName string
	}{
		{name: "file carries both", payload: `{"name":"from file","brand_id":9,"locations":[]}`, wantName: "from file"},
		{name: "flags override the file", payload: `{"name":"from file","brand_id":9}`,
			setName: true, setBrand: true, wantName: "from flag"},
		{name: "flag supplies a missing name", payload: `{"brand_id":9}`, setName: true, wantName: "from flag"},
		{name: "no name anywhere", payload: `{"brand_id":9}`, wantErr: "--name"},
		{name: "no brand anywhere", payload: `{"name":"from file"}`, wantErr: "--brand-id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, err := mergeSubmissionFields([]byte(tc.payload), "from flag", 77, tc.setName, tc.setBrand)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if body["name"] != tc.wantName {
				t.Errorf("name = %v, want %v", body["name"], tc.wantName)
			}
			if tc.setBrand && body["brand_id"] != 77 {
				t.Errorf("brand_id = %v, want 77", body["brand_id"])
			}
		})
	}
}

func TestPoiSubmissionsCreateByListPostsTheMergedBody(t *testing.T) {
	srv, got := stub(t, submissionEnvelope)

	path := filepath.Join(t.TempDir(), "locations.json")
	if err := os.WriteFile(path,
		[]byte(`{"name":"from file","brand_id":9,"country_isocode":"alpha_2",`+
			`"locations":[{"country":"US","longitude":-89.6501,"latitude":39.7817}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, poiSubmissionCreateCommand(), srv, "--list", path, "--name", "from flag")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/my-data/pois/submissions/create-by-list" {
		t.Errorf("path = %s", got.paths[0])
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "from flag" {
		t.Errorf("--name should win over the file, got %v", body["name"])
	}
	// Fields the merge knows nothing about must survive it untouched.
	if body["country_isocode"] != "alpha_2" {
		t.Errorf("country_isocode = %v", body["country_isocode"])
	}
	if locs, _ := body["locations"].([]any); len(locs) != 1 {
		t.Errorf("locations = %v", body["locations"])
	}
}

// The list route merges the file with the flags and used to stop there: a
// --update --key that posted no update/key made the server insert duplicates.
func TestPoiSubmissionsCreateByListCarriesTheMatchFields(t *testing.T) {
	srv, got := stub(t, submissionEnvelope)

	path := filepath.Join(t.TempDir(), "locations.json")
	if err := os.WriteFile(path,
		[]byte(`{"name":"from file","brand_id":9,`+
			`"locations":[{"country":"US","longitude":-89.6501,"latitude":39.7817}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := run(t, poiSubmissionCreateCommand(), srv,
		"--list", path, "--update", "--key", "store-id")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatal(err)
	}
	// create-by-list is JSON, so these are real booleans like create-by-upload.
	if body["update"] != true {
		t.Errorf("update = %#v, want the JSON boolean true", body["update"])
	}
	if body["key"] != "store-id" {
		t.Errorf("key = %#v, want store-id", body["key"])
	}
	if _, ok := body["remove"]; ok {
		t.Errorf("unset --remove leaked: %v", body)
	}
}

// --------------------------------------------------------------------------------- locations

func TestPoiLocationsListBuildsTheQuery(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	_, _, err := run(t, poiLocationsCommand(), srv, "list",
		"--search", "example coffee",
		"--brands", "5,6",
		"--countries", "US",
		"--countries", "CA",
		"--geometry", "polygon")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/my-data/pois/index" {
		t.Errorf("path = %s", got.paths[0])
	}

	q, err := url.ParseQuery(got.queries[0])
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("search") != "example coffee" {
		t.Errorf("search = %q", q.Get("search"))
	}
	// A comma-separated --brands is two ids, and each goes out as its own entry.
	if b := q["brands[]"]; len(b) != 2 || b[0] != "5" || b[1] != "6" {
		t.Errorf("brands[] = %v", b)
	}
	if c := q["countries[]"]; len(c) != 2 || c[1] != "CA" {
		t.Errorf("countries[] = %v", c)
	}
	// --geometry is sent under the name the API uses.
	if q.Get("type") != "polygon" {
		t.Errorf("type = %q", q.Get("type"))
	}
	// Unset paging must not go out as zeroes.
	for _, k := range []string{"page", "per_page"} {
		if q.Has(k) {
			t.Errorf("unset --%s leaked: %s", k, got.queries[0])
		}
	}
}

// Same rule as the reference reads: page=0 is a 422, so the flag refuses it.
func TestPoiLocationsListPageFlagsRefuseZero(t *testing.T) {
	for _, args := range [][]string{{"--page", "0"}, {"--per-page", "0"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			srv, got := stub(t, `{}`)

			_, _, err := run(t, poiLocationsCommand(), srv, append([]string{"list"}, args...)...)
			if err == nil || !strings.Contains(err.Error(), "must be 1 or more") {
				t.Fatalf("err = %v, want the flag to refuse it", err)
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

func TestPoiLocationsListRejectsABadGeometry(t *testing.T) {
	srv, got := stub(t, `{}`)

	_, _, err := run(t, poiLocationsCommand(), srv, "list", "--geometry", "hexagon")
	if err == nil || !strings.Contains(err.Error(), "polygon or coordinates") {
		t.Fatalf("err = %v", err)
	}
	if exitCode(err, nil, true) != 2 {
		t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
	}
	if len(got.paths) != 0 {
		t.Errorf("should cost no round trip, got %v", got.paths)
	}
}

// --------------------------------------------------------------------------------- submissions index

// This index speaks its own dialect - q, sortBy, orderBy - while the flags stay
// in the CLI's idiom. The mapping is the thing worth pinning down.
func TestPoiSubmissionsListMapsItsOwnParameterNames(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	_, _, err := run(t, poiSubmissionsCommand(), srv, "list",
		"--search", "cli-task-7", "--sort-by", "created_at", "--order", "desc")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	q, err := url.ParseQuery(got.queries[0])
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("q") != "cli-task-7" {
		t.Errorf("--search should be sent as q, got %s", got.queries[0])
	}
	if q.Get("sortBy") != "created_at" || q.Get("orderBy") != "desc" {
		t.Errorf("query = %s", got.queries[0])
	}
}

func TestPoiSubmissionsListRejectsBadSorts(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown column", []string{"list", "--sort-by", "size"}, "--sort-by must be"},
		{"unknown direction", []string{"list", "--order", "sideways"}, "--order must be"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{}`)

			_, _, err := run(t, poiSubmissionsCommand(), srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v", err)
			}
			if exitCode(err, nil, true) != 2 {
				t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// --------------------------------------------------------------------------------- delete

func TestPoiSubmissionsDeleteAbortsOnNo(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	cmd := poiSubmissionDeleteCommand()
	cmd.SetIn(strings.NewReader("n\n"))
	_, errb, err := run(t, cmd, srv, "31")

	if err == nil || err.Error() != "aborted" {
		t.Fatalf("err = %v, want aborted", err)
	}
	if len(got.paths) != 0 {
		t.Errorf("an aborted delete must send nothing, got %v", got.paths)
	}
	if !strings.Contains(errb, "Delete submission 31?") {
		t.Errorf("prompt = %q", errb)
	}
}

func TestPoiSubmissionsDeleteWithYesPostsTheID(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	_, _, err := run(t, poiSubmissionDeleteCommand(), srv, "31", "--yes")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.paths[0] != "/api/v2/my-data/pois/submissions/delete-by-id" {
		t.Errorf("path = %s", got.paths[0])
	}

	var body map[string]int
	if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
		t.Fatalf("body %q: %v", got.bodies[0], err)
	}
	if body["id"] != 31 {
		t.Errorf("body = %v", body)
	}
}

// --------------------------------------------------------------------------------- taxonomy

// Two creates that look alike but are not: the parent id changes field name
// with the noun, and getting them crossed is silent at the CLI layer.
func TestPoiTaxonomyCreatesNameTheirParentField(t *testing.T) {
	tests := []struct {
		name      string
		build     func() *cobra.Command
		args      []string
		wantPath  string
		wantField string
	}{
		{
			name: "brand under a category",
			build: func() *cobra.Command {
				return poiCreateCommand("create", "", poiPrefix+"/brands/create",
					"category-id", "", "", []string{"id", "name"})
			},
			args:      []string{"--name", "example-brand", "--category-id", "12"},
			wantPath:  "/api/v2/my-data/pois/brands/create",
			wantField: "category_id",
		},
		{
			name: "category under a segment",
			build: func() *cobra.Command {
				return poiCreateCommand("create", "", poiPrefix+"/categories/create",
					"segment-id", "", "", []string{"id", "name"})
			},
			args:      []string{"--name", "example-category", "--segment-id", "12"},
			wantPath:  "/api/v2/my-data/pois/categories/create",
			wantField: "segment_id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{"status":"success","code":201,"data":[{"id":1,"name":"x"}]}`)

			if _, _, err := run(t, tc.build(), srv, tc.args...); err != nil {
				t.Fatalf("execute: %v", err)
			}
			if got.paths[0] != tc.wantPath {
				t.Errorf("path = %s, want %s", got.paths[0], tc.wantPath)
			}

			var body map[string]any
			if err := json.Unmarshal([]byte(got.bodies[0]), &body); err != nil {
				t.Fatal(err)
			}
			if body[tc.wantField] != float64(12) {
				t.Errorf("%s = %v, want 12", tc.wantField, body[tc.wantField])
			}
		})
	}
}

func TestPoiSegmentsListOmitsAnUnsetSearch(t *testing.T) {
	srv, got := stub(t, `{"status":"success","code":200,"data":[]}`)

	_, _, err := run(t, searchList("segments", "", poiPrefix+"/segments/index",
		"no segments", []string{"id", "name"}), srv)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got.queries[0] != "" {
		t.Errorf("an unset --search leaked: %q", got.queries[0])
	}
}

// MarkFlagRequired checks presence, not content: --name "" and --segment-id 0
// both satisfied it and went out as a body the server rejects.
func TestPoiTaxonomyCreatesRejectEmptyNameAndZeroParent(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"empty name", []string{"--name", "", "--segment-id", "12"}, "--name"},
		{"zero parent", []string{"--name", "x", "--segment-id", "0"}, "--segment-id"},
		{"negative parent", []string{"--name", "x", "--segment-id", "-3"}, "--segment-id"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{}`)

			cmd := poiCreateCommand("create", "", poiPrefix+"/categories/create",
				"segment-id", "", "", []string{"id", "name"})
			_, _, err := run(t, cmd, srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to name %s", err, tc.want)
			}
			if exitCode(err, nil, true) != 2 {
				t.Errorf("exit = %d, want 2", exitCode(err, nil, true))
			}
			if len(got.paths) != 0 {
				t.Errorf("should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The three taxonomy nouns read the same way: 'poi segments list' used to be
// the odd one out, a leaf where categories and brands were groups.
func TestPoiTaxonomyNounsAllHaveList(t *testing.T) {
	for _, noun := range []string{"segments", "categories", "brands"} {
		group, _, err := poiCmd.Find([]string{noun})
		if err != nil || group.Name() != noun {
			t.Fatalf("poi %s: %v", noun, err)
		}
		if group.Runnable() {
			t.Errorf("poi %s is a leaf; want a group holding list", noun)
		}
		list, _, err := poiCmd.Find([]string{noun, "list"})
		if err != nil || list.Name() != "list" || !list.Runnable() {
			t.Errorf("poi %s list is missing: %v", noun, err)
		}
	}
}

// --file bypasses createBody, so --json needs its own case: a script reading
// data[0].value must get the envelope, not a table.
func TestPoiSubmissionsCreateByFileHonoursJSON(t *testing.T) {
	srv, _ := stub(t, submissionEnvelope)

	out, _, err := runJSON(t, poiSubmissionCreateCommand(), srv,
		"--name", "cli-task-8-test",
		"--brand-id", "9",
		"--file", csvFile(t))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	var env struct {
		Status string `json:"status"`
		Data   []struct {
			Value json.Number `json:"value"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--json did not print a parseable envelope: %v\n%s", err, out)
	}
	if env.Status != "success" || len(env.Data) == 0 || env.Data[0].Value == "" {
		t.Fatalf("envelope missing data[0].value: %s", out)
	}
}

// Only --list can take these from the file; the other two must fail offline
// rather than spend a round trip earning a 422.
func TestPoiSubmissionsCreateNeedsNameAndBrand(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"file, no name", []string{"--brand-id", "9", "--file", "a.csv"}, "--name"},
		{"file, no brand", []string{"--name", "x", "--file", "a.csv"}, "--brand-id"},
		{"file, neither", []string{"--file", "a.csv"}, "--name and --brand-id"},
		{"upload, neither", []string{"--upload-reference", "upl_1"}, "--name and --brand-id"},
		{"empty name is still missing", []string{"--name", "", "--brand-id", "9", "--file", "a.csv"}, "--name"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, got := stub(t, `{}`)

			_, _, err := run(t, poiSubmissionCreateCommand(), srv, tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			if len(got.paths) != 0 {
				t.Errorf("a missing required field should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The exception MarkFlagRequired would have broken: --list may omit both.
func TestPoiSubmissionsCreateByListNeedsNoFlags(t *testing.T) {
	srv, got := stub(t, submissionEnvelope)

	path := filepath.Join(t.TempDir(), "locations.json")
	if err := os.WriteFile(path,
		[]byte(`{"name":"from file","brand_id":9,`+
			`"locations":[{"country":"US","longitude":-89.6501,"latitude":39.7817}]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := run(t, poiSubmissionCreateCommand(), srv, "--list", path); err != nil {
		t.Fatalf("--list without --name/--brand-id should work: %v", err)
	}
	if len(got.paths) != 1 {
		t.Fatalf("paths = %v", got.paths)
	}
}
