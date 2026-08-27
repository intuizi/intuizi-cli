package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// referenceEndpoints returns every row of the table, keyed group/item.
func referenceEndpoints(t *testing.T) map[string]endpoint {
	t.Helper()

	all := make(map[string]endpoint)
	for _, g := range referenceGroups {
		for _, e := range g.endpoints {
			key := g.name + "/" + e.item
			if _, dup := all[key]; dup {
				t.Fatalf("duplicate command %q", key)
			}
			all[key] = e
		}
	}
	return all
}

// wantPaths is the 36 reads the CLI exposes. A row added or dropped without
// updating this list fails the coverage test below.
var wantPaths = []string{
	"/analyses/reference/affinity-transactions/brands",
	"/analyses/reference/affinity-transactions/categories",
	"/analyses/reference/affinity-transactions/subcategories",
	"/analyses/reference/apps/bundle-ids",
	"/analyses/reference/apps/categories",
	"/analyses/reference/apps/os",
	"/analyses/reference/apps/tags",
	"/analyses/reference/apps/taxonomies",
	"/analyses/reference/cohorts/get-cohorts",
	"/analyses/reference/common/cities",
	"/analyses/reference/common/countries",
	"/analyses/reference/common/dataset-types",
	"/analyses/reference/common/dmas",
	"/analyses/reference/common/operators",
	"/analyses/reference/common/states",
	"/analyses/reference/common/zipcodes",
	"/analyses/reference/ctv/channel-names",
	"/analyses/reference/ctv/connection-types",
	"/analyses/reference/ctv/content-genres",
	"/analyses/reference/ctv/content-types",
	"/analyses/reference/ctv/device-makes",
	"/analyses/reference/ctv/device-oses",
	"/analyses/reference/ctv/device-types",
	"/analyses/reference/ctv/isps",
	"/analyses/reference/ctv/series",
	"/analyses/reference/ctv/vendors",
	"/analyses/reference/poi/brands",
	"/analyses/reference/poi/categories",
	"/analyses/reference/poi/locations",
	"/analyses/reference/poi/segments",
	"/analyses/reference/web/browsers",
	"/analyses/reference/web/device-makes",
	"/analyses/reference/web/device-oses",
	"/analyses/reference/web/device-types",
	"/analyses/reference/web/domains",
	"/analyses/reference/web/ref-domains",
}

func TestReferenceCoversEveryScopedPath(t *testing.T) {
	built := make(map[string]bool)
	for _, g := range referenceGroups {
		for _, e := range g.endpoints {
			segment := e.path
			if segment == "" {
				segment = e.item
			}
			built[referencePrefix+g.name+"/"+segment] = true
		}
	}

	for _, want := range wantPaths {
		if !built[want] {
			t.Errorf("no command builds %s", want)
		}
		delete(built, want)
	}
	for extra := range built {
		t.Errorf("command builds %s, which is not in wantPaths", extra)
	}
	if len(wantPaths) != 36 {
		t.Errorf("wantPaths has %d entries, want 36", len(wantPaths))
	}
}

func TestReferenceTableIsWellFormed(t *testing.T) {
	for key, e := range referenceEndpoints(t) {
		if e.short == "" {
			t.Errorf("%s: empty Short, so it is invisible in --help", key)
		}
		if e.noSearch && (len(e.params) > 0 || e.paged) {
			t.Errorf("%s: noSearch means the read takes no parameters at all", key)
		}
		seen := make(map[string]bool)
		seenFlags := make(map[string]bool)
		for _, p := range e.params {
			if p.name == "" {
				t.Errorf("%s: param with no name", key)
			}
			if seen[p.name] {
				t.Errorf("%s: duplicate param %q", key, p.name)
			}
			seen[p.name] = true

			// Two params that collide on flag name panic pflag, and every
			// command is built in init(), so the whole binary would die at
			// startup - not just this one command.
			if flag := flagName(p.name); seenFlags[flag] {
				t.Errorf("%s: param %q collides on flag name --%s", key, p.name, flag)
			} else {
				seenFlags[flag] = true
			}
			if p.help == "" {
				t.Errorf("%s: param %q has no help text", key, p.name)
			}
			// These come from the paged/noSearch fields; listing them as params
			// would register the flag twice and panic.
			switch p.name {
			case "search", "page", "per_page":
				t.Errorf("%s: %q is added automatically, drop it from params", key, p.name)
			}
		}
	}
}

// TestReferenceRequiredParams pins the four reads with required parameters.
func TestReferenceRequiredParams(t *testing.T) {
	want := map[string][]string{
		"common/states":   {"countries"},
		"common/cities":   {"states"},
		"common/dmas":     {"countries"},
		"common/zipcodes": {"cities"},
	}

	for key, e := range referenceEndpoints(t) {
		var got []string
		for _, p := range e.params {
			if p.required {
				got = append(got, p.name)
			}
		}
		if strings.Join(got, ",") != strings.Join(want[key], ",") {
			t.Errorf("%s: required params are %v, want %v", key, got, want[key])
		}
	}
}

func TestFlagName(t *testing.T) {
	cases := map[string]string{
		"countries":         "countries",
		"category_ids":      "category-ids",
		"datasetType":       "dataset-type",
		"dataType":          "data-type",
		"partner_id":        "partner-id",
		"subcategory_codes": "subcategory-codes",
	}
	for in, want := range cases {
		if got := flagName(in); got != want {
			t.Errorf("flagName(%q) = %q, want %q", in, got, want)
		}
	}
}

// serve stands in for the API, recording each request's query.
func serve(t *testing.T, body string) (*httptest.Server, *[]string) {
	t.Helper()

	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	return srv, &queries
}

// runCmd executes one endpoint's command against a fake server. Commands are
// built fresh per call: a reused cobra command reports old flags as Changed.
func runCmd(t *testing.T, group string, e endpoint, baseURL string, args ...string) string {
	t.Helper()

	t.Setenv("INTUIZI_API_TOKEN", "test-token")
	previous := baseURLFlag
	baseURLFlag = baseURL
	t.Cleanup(func() { baseURLFlag = previous })

	cmd := e.command(group)
	var stdout, stderr strings.Builder
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	cmd.SetContext(context.Background())

	if err := cmd.Execute(); err != nil {
		t.Fatalf("%s %s: %v (stderr: %s)", group, e.item, err, stderr.String())
	}
	return stdout.String()
}

const flatBody = `{"status":"success","code":200,"message":"ok","data":[{"value":"US","text":"United States (US)"}]}`

func TestQueryBuilding(t *testing.T) {
	tests := []struct {
		name  string
		group string
		item  string
		args  []string
		want  string
	}{
		{
			name:  "no flags sends no query at all",
			group: "common", item: "countries",
			want: "",
		},
		{
			name:  "scalar param keeps the API's own spelling",
			group: "common", item: "countries",
			args: []string{"--dataset-type", "WebDomain"},
			want: "datasetType=WebDomain",
		},
		{
			name:  "string slices repeat under a bracketed key",
			group: "common", item: "states",
			args: []string{"--countries", "US", "--countries", "CA"},
			want: "countries%5B%5D=US&countries%5B%5D=CA",
		},
		{
			name:  "a value containing a comma is one value, not two",
			group: "common", item: "cities",
			args: []string{"--states", "NY", "--search", "Springfield, IL"},
			want: "search=Springfield%2C+IL&states%5B%5D=NY",
		},
		{
			name:  "int slices accept a comma-separated list",
			group: "poi", item: "categories",
			args: []string{"--segments", "1,2"},
			want: "segments%5B%5D=1&segments%5B%5D=2",
		},
		{
			name:  "pagination flags use the API's snake_case",
			group: "apps", item: "categories",
			args: []string{"--page", "2", "--per-page", "50"},
			want: "page=2&per_page=50",
		},
		{
			name:  "an empty --search is still sent, because the user asked for it",
			group: "ctv", item: "vendors",
			args: []string{"--search", ""},
			want: "search=",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, queries := serve(t, flatBody)
			e := findEndpoint(t, tc.group, tc.item)
			runCmd(t, tc.group, e, srv.URL, tc.args...)

			if len(*queries) != 1 {
				t.Fatalf("got %d requests, want 1", len(*queries))
			}
			if (*queries)[0] != tc.want {
				t.Errorf("query = %q, want %q", (*queries)[0], tc.want)
			}
		})
	}
}

func TestUnsetPaginationFlagsAreNotSent(t *testing.T) {
	srv, queries := serve(t, flatBody)
	e := findEndpoint(t, "apps", "categories")
	runCmd(t, "apps", e, srv.URL)

	// per_page=0 is a validation error, so an untouched flag must stay off the wire.
	if (*queries)[0] != "" {
		t.Errorf("query = %q, want empty", (*queries)[0])
	}
}

func TestMissingRequiredFlagIsAUsageError(t *testing.T) {
	t.Setenv("INTUIZI_API_TOKEN", "test-token")

	cmd := findEndpoint(t, "common", "states").command("common")
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs(nil)
	cmd.SetContext(context.Background())

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected an error when --countries is missing")
	}
	if !strings.Contains(err.Error(), "countries") {
		t.Errorf("error should name the missing flag, got %v", err)
	}
}

func TestJSONPrintsTheEnvelopeNotJustData(t *testing.T) {
	srv, _ := serve(t, flatBody)

	jsonOutput = true
	t.Cleanup(func() { jsonOutput = false })

	out := runCmd(t, "common", findEndpoint(t, "common", "countries"), srv.URL)

	for _, key := range []string{`"status"`, `"code"`, `"message"`, `"data"`} {
		if !strings.Contains(out, key) {
			t.Errorf("output is missing %s:\n%s", key, out)
		}
	}
}

func TestCohortsListUsesTheGetCohortsSegment(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, flatBody)
	}))
	t.Cleanup(srv.Close)

	runCmd(t, "cohorts", findEndpoint(t, "cohorts", "list"), srv.URL)

	if want := "/api/v2/analyses/reference/cohorts/get-cohorts"; path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
}

func findEndpoint(t *testing.T, group, item string) endpoint {
	t.Helper()

	for _, g := range referenceGroups {
		if g.name != group {
			continue
		}
		for _, e := range g.endpoints {
			if e.item == item {
				return e
			}
		}
	}
	t.Fatalf("no endpoint %s/%s in the table", group, item)
	return endpoint{}
}

// No current row uses an int scalar or noSearch, but the builder supports both
// so a new row stays one line. Tested against synthetic endpoints.
func TestBuilderHandlesIntScalarParams(t *testing.T) {
	srv, queries := serve(t, flatBody)

	e := endpoint{item: "pricing-models", short: "synthetic", params: []param{
		{name: "partner_id", kind: num, help: "Endpoint partner id"},
	}}
	runCmd(t, "common", e, srv.URL, "--partner-id", "3")

	// Scalars are not bracketed: only slice params repeat under name[].
	if (*queries)[0] != "partner_id=3" {
		t.Errorf("query = %q, want %q", (*queries)[0], "partner_id=3")
	}
}

func TestBuilderOmitsSearchWhenNoSearch(t *testing.T) {
	e := endpoint{item: "recency-limits", short: "synthetic", noSearch: true}
	cmd := e.command("profile-attributes")

	if cmd.Flags().Lookup("search") != nil {
		t.Error("a noSearch read should not register --search")
	}
}

// dummy is a stand-in value for a required flag.
func dummy(k kind) string {
	switch k {
	case num:
		return "3"
	case nums:
		return "1"
	default:
		return "US"
	}
}

// The two response shapes the reference reads use.
const (
	flatEnvelope  = `{"status":"success","code":200,"message":"Resources fetched successfully.","data":[{"value":"US","text":"United States (US)"}]}`
	pagedEnvelope = `{"status":"success","code":200,"message":"Resources fetched successfully.","data":{"items":[{"value":"US","text":"United States (US)"}],"pagination":{"current_page":1,"per_page":500,"total":1,"last_page":1}}}`
)

// TestEveryScopedEndpointWorksWithJSON is the ticket's done-when: every endpoint
// reachable, each printing a usable envelope under --json.
func TestEveryScopedEndpointWorksWithJSON(t *testing.T) {
	for _, g := range referenceGroups {
		for _, e := range g.endpoints {
			t.Run(g.name+"/"+e.item, func(t *testing.T) {
				body := flatEnvelope
				if e.paged {
					body = pagedEnvelope
				}

				var gotPath string
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					gotPath = r.URL.Path
					w.Header().Set("Content-Type", "application/json")
					io.WriteString(w, body)
				}))
				t.Cleanup(srv.Close)

				var args []string
				for _, p := range e.params {
					if p.required {
						args = append(args, "--"+flagName(p.name), dummy(p.kind))
					}
				}

				jsonOutput = true
				t.Cleanup(func() { jsonOutput = false })

				out := runCmd(t, g.name, e, srv.URL, args...)

				segment := e.path
				if segment == "" {
					segment = e.item
				}
				wantPath := "/api/v2" + referencePrefix + g.name + "/" + segment
				if gotPath != wantPath {
					t.Errorf("hit %s, want %s", gotPath, wantPath)
				}

				// The whole envelope, parseable: what makes '| jq .data' work.
				var env struct {
					Status  string          `json:"status"`
					Code    int             `json:"code"`
					Message string          `json:"message"`
					Data    json.RawMessage `json:"data"`
				}
				if err := json.Unmarshal([]byte(out), &env); err != nil {
					t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
				}
				if env.Status != "success" || env.Code != 200 {
					t.Errorf("envelope lost its status/code: %+v", env)
				}
				if len(env.Data) == 0 {
					t.Error("envelope has no data field")
				}
			})
		}
	}
}

// A paginated read fetches one page and says so on stderr.
func TestPagedReadFetchesExactlyOnePage(t *testing.T) {
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, pagedEnvelopeMultiPage)
	}))
	t.Cleanup(srv.Close)

	e := findEndpoint(t, "apps", "categories")
	out := runCmd(t, "apps", e, srv.URL)

	if requests != 1 {
		t.Errorf("made %d requests, want exactly 1", requests)
	}
	// Page 1 only, not the 3881310 rows the envelope claims exist.
	if lines := strings.Count(strings.TrimSpace(out), "\n") + 1; lines != 2 {
		t.Errorf("got %d output lines, want 2:\n%s", lines, out)
	}
}

func TestNoAllFlagIsRegistered(t *testing.T) {
	for _, g := range referenceGroups {
		for _, e := range g.endpoints {
			if e.command(g.name).Flags().Lookup("all") != nil {
				t.Errorf("%s/%s registers --all; reads are one page per call", g.name, e.item)
			}
		}
	}
}

const pagedEnvelopeMultiPage = `{"status":"success","code":200,"message":"ok","data":{"items":[{"value":"X","text":"X"}],"pagination":{"current_page":1,"per_page":500,"total":3881310,"last_page":7763}}}`
