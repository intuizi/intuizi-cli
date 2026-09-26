package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// catalogServer answers every request with body and counts them.
func catalogServer(t *testing.T, body string) (*api.Client, *int) {
	t.Helper()

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)

	return api.New(srv.URL, "test-token"), &calls
}

// Both envelope shapes: paged catalogs answer with data.items, unpaged ones
// with a bare array.
func TestResolveOneTakesBothEnvelopeShapes(t *testing.T) {
	for name, body := range map[string]string{
		"flat":  `{"status":"success","code":200,"message":"ok","data":[{"value":208,"text":"Starbucks"}]}`,
		"paged": `{"status":"success","code":200,"message":"ok","data":{"items":[{"value":208,"text":"Starbucks"}],"pagination":{"current_page":1,"per_page":250,"total":1,"last_page":1}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := catalogServer(t, body)

			got, err := resolveOne(context.Background(), c, brandsPath, "starbucks", "brands")
			if err != nil {
				t.Fatalf("resolveOne: %v", err)
			}
			// Compare as JSON: the decoder's Go type is incidental.
			raw, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshalling the resolved id: %v", err)
			}
			if string(raw) != "208" {
				t.Errorf("resolved to %s, want 208", raw)
			}
		})
	}
}

// No match names the search term rather than selecting nothing.
func TestResolveOneRejectsNoMatch(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	_, err := resolveOne(context.Background(), c, brandsPath, "nosuchbrand", "brands")
	if err == nil {
		t.Fatal("resolveOne accepted a search with no matches")
	}
	if !strings.Contains(err.Error(), "nosuchbrand") {
		t.Errorf("error does not name the search term: %v", err)
	}
}

// Several matches list what was found. Picking one silently is the failure
// that builds an audience with zero devices.
func TestResolveOneRejectsAmbiguity(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":929,"text":"Dutch Bros Coffee"},{"value":921,"text":"Gregorys Coffee"}]}`)

	_, err := resolveOne(context.Background(), c, brandsPath, "coffee", "brands")
	if err == nil {
		t.Fatal("resolveOne picked one of several matches")
	}
	// Ids as well as labels: the fix is usually to pass the id.
	for _, want := range []string{"929", "Dutch Bros Coffee", "921", "Gregorys Coffee", "2 brands"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
	// One per line, so a long list stays readable.
	if lines := strings.Count(err.Error(), "\n"); lines != 2 {
		t.Errorf("got %d newlines, want one per candidate:\n%v", lines, err)
	}
}

// Catalogs that answer {id,name} instead of {value,text} must list ids and
// labels too, not a column of <nil>.
func TestResolveOneAmbiguityListsIDNameRows(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"id":200,"name":"Brand 0"},{"id":201,"name":"Brand 1"}]}`)

	_, err := resolveOne(context.Background(), c, brandsPath, "brand", "brands")
	if err == nil {
		t.Fatal("resolveOne picked one of several matches")
	}
	for _, want := range []string{"200", "Brand 0", "201", "Brand 1"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "<nil>") {
		t.Errorf("error renders nil cells:\n%v", err)
	}
}

// A paged catalog answers one page. Listing it as if it were everything hides
// the matches that would have made the search obviously too broad.
func TestResolveOneAmbiguitySaysWhenMoreMatchesExist(t *testing.T) {
	page := func(total int) string {
		return `{"status":"success","code":200,"message":"ok","data":{"items":[` +
			`{"value":1,"text":"Games A"},{"value":2,"text":"Games B"}],` +
			`"pagination":{"current_page":1,"per_page":2,"total":` + strconv.Itoa(total) + `,"last_page":1}}}`
	}
	appsCategories := referencePrefix + "apps/categories"

	t.Run("truncated", func(t *testing.T) {
		c, _ := catalogServer(t, page(30))
		_, err := resolveOne(context.Background(), c, appsCategories, "games", "categories")
		if err == nil {
			t.Fatal("resolveOne picked one of several matches")
		}
		for _, want := range []string{"showing 2 of 30", "narrow the search", "Games A", "Games B"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error omits %q: %v", want, err)
			}
		}
		// The hint must not cost the one-line-per-candidate layout.
		if lines := strings.Count(err.Error(), "\n"); lines != 2 {
			t.Errorf("got %d newlines, want one per candidate:\n%v", lines, err)
		}
	})

	t.Run("complete page", func(t *testing.T) {
		c, _ := catalogServer(t, page(2))
		_, err := resolveOne(context.Background(), c, appsCategories, "games", "categories")
		if err == nil {
			t.Fatal("resolveOne picked one of several matches")
		}
		if strings.Contains(err.Error(), "showing") {
			t.Errorf("a complete page needs no truncation hint: %v", err)
		}
	})
}

// Zero and negative numbers parse as ids, but no catalog holds them, so they
// would build an empty audience. Rejected locally, like parseID.
func TestResolveOrIDRejectsNonPositiveIDs(t *testing.T) {
	c, calls := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	for _, bad := range []string{"0", "-5"} {
		_, err := resolveOrID(context.Background(), c, brandsPath, "--brand", bad, "brands")
		if err == nil {
			t.Errorf("resolveOrID accepted %q as an id", bad)
			continue
		}
		var ue usageError
		if !errors.As(err, &ue) {
			t.Errorf("%q: not a usage error: %v", bad, err)
		}
		if !strings.Contains(err.Error(), "--brand") || !strings.Contains(err.Error(), bad) {
			t.Errorf("error should name the flag and the value: %v", err)
		}
	}
	if *calls != 0 {
		t.Errorf("made %d requests for a bad id, want 0", *calls)
	}
}

// A numeric argument is the id itself and costs no round trip.
func TestResolveOrIDSkipsTheLookupForANumber(t *testing.T) {
	c, calls := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	got, err := resolveOrID(context.Background(), c, brandsPath, "--brand", "208", "brands")
	if err != nil {
		t.Fatalf("resolveOrID: %v", err)
	}
	if got != 208 {
		t.Errorf("got %v, want 208", got)
	}
	if *calls != 0 {
		t.Errorf("made %d requests for a literal id, want 0", *calls)
	}
}

// A non-numeric argument goes through the catalog.
func TestResolveOrIDLooksUpAName(t *testing.T) {
	c, calls := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[{"value":208,"text":"Starbucks"}]}`)

	if _, err := resolveOrID(context.Background(), c, brandsPath, "--brand", "starbucks", "brands"); err != nil {
		t.Fatalf("resolveOrID: %v", err)
	}
	if *calls != 1 {
		t.Errorf("made %d requests for a name, want 1", *calls)
	}
}

// Every provider, in order; an empty catalog is an error.
func TestAllProviders(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":"aaa","text":"aaa"},{"value":"bbb","text":"bbb"}]}`)

	got, err := allProviders(context.Background(), c, "POI")
	if err != nil {
		t.Fatalf("allProviders: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling providers: %v", err)
	}
	if string(raw) != `["aaa","bbb"]` {
		t.Errorf("got %s, want [\"aaa\",\"bbb\"]", raw)
	}
}

func TestAllProvidersRejectsAnEmptyCatalog(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	if _, err := allProviders(context.Background(), c, "POI"); err == nil {
		t.Fatal("allProviders accepted a dataset type with no providers")
	}
}

func TestParseWindow(t *testing.T) {
	for name, tc := range map[string]struct {
		start, end string
		wantErr    string
	}{
		"valid":        {"2026-09-02", "2026-09-09", ""},
		"same day":     {"2026-09-02", "2026-09-02", ""},
		"transposed":   {"2026-09-09", "2026-09-02", "before"},
		"bad start":    {"09/02/2026", "2026-09-09", "--start-date"},
		"bad end":      {"2026-09-02", "next tuesday", "--end-date"},
		"empty is bad": {"", "2026-09-09", "--start-date"},
	} {
		t.Run(name, func(t *testing.T) {
			err := parseWindow(tc.start, tc.end)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("parseWindow(%q, %q): %v", tc.start, tc.end, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("parseWindow(%q, %q) accepted an invalid window", tc.start, tc.end)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// Documented field names, and unset selectors drop out rather than ship null.
// Case-insensitive in, canonical out: the API's spelling is not derivable.
func TestCanonicalType(t *testing.T) {
	for in, want := range map[string]string{
		"poi":                  "POI",
		"POI":                  "POI",
		"PoI":                  "POI",
		"apps":                 "Apps",
		"webdomain":            "WebDomain",
		"affinitytransactions": "AffinityTransactions",
		"profileattributes":    "ProfileAttributes",
		"origin":               "Origin",
		"ORIGIN":               "Origin",
	} {
		got, err := canonicalType(in)
		if err != nil {
			t.Errorf("canonicalType(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("canonicalType(%q) = %q, want %q", in, got, want)
		}
	}
}

// An unknown type names the valid ones rather than reaching the API.
func TestCanonicalTypeRejectsUnknown(t *testing.T) {
	_, err := canonicalType("pois")
	if err == nil {
		t.Fatal("canonicalType accepted an unknown type")
	}
	for _, want := range []string{"pois", "POI", "AffinityTransactions"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
}

func TestResolveOneRejectsARowWithNoID(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[{"text":"Starbucks"}]}`)

	if _, err := resolveOne(context.Background(), c, brandsPath, "starbucks", "brands"); err == nil {
		t.Fatal("resolveOne accepted a row carrying neither value nor id")
	}
}

// Catalogs answer with value/text or id/name; both are ids.
func TestCatalogValueReadsEitherKey(t *testing.T) {
	for name, r := range map[string]output.Record{
		"value": {"value": 208, "text": "Starbucks"},
		"id":    {"id": 3, "name": "Custom"},
	} {
		got, err := catalogValue(r, "/x")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if got == nil {
			t.Errorf("%s: got nil", name)
		}
	}
	if _, err := catalogValue(output.Record{"text": "no id here"}, "/x"); err == nil {
		t.Error("catalogValue accepted a row with neither key")
	}
}

// IAB category rows carry the code as value and the catalog id as id, and
// Create Audience takes the id: iab_category_codes are integers. Every other
// catalog's value is the id itself.
func TestCatalogValueTakesTheIDForIABCategories(t *testing.T) {
	row := output.Record{"value": "IAB2", "text": "IAB2 - Automotive", "id": 2}

	got, err := catalogValue(row, referencePrefix+"web/iab-categories")
	if err != nil {
		t.Fatalf("catalogValue: %v", err)
	}
	if raw, _ := json.Marshal(got); string(raw) != "2" {
		t.Errorf("IAB category resolved to %s, want the id 2", raw)
	}

	got, err = catalogValue(row, brandsPath)
	if err != nil {
		t.Fatalf("catalogValue: %v", err)
	}
	if got != "IAB2" {
		t.Errorf("another catalog resolved to %v, want its value", got)
	}
}

// The "pass an id" list must show what --category takes, so an IAB category
// lists its id, not the code.
func TestResolveOneAmbiguityListsIABCategoryIDs(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":"IAB2","text":"IAB2 - Automotive","id":2},`+
		`{"value":"IAB20","text":"IAB20 - Travel","id":20}]}`)

	_, err := resolveOne(context.Background(), c, referencePrefix+"web/iab-categories", "IAB", "categories")
	if err == nil {
		t.Fatal("resolveOne picked one of several matches")
	}
	for _, want := range []string{"\n  2 ", "\n  20 ", "IAB2 - Automotive", "IAB20 - Travel"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
	for _, code := range []string{"\n  IAB2 ", "\n  IAB20 "} {
		if strings.Contains(err.Error(), code) {
			t.Errorf("error lists the code %q where the id belongs: %v", code, err)
		}
	}
}

// An IAB category is labelled "CODE - name", so neither its code nor its name
// is ever the whole label. A code that other codes contain, such as IAB1, and
// a name that other names contain used to fail with a match list; the row
// whose code or name is exactly the one given is taken instead.
func TestResolveOneTakesAnExactIABCodeOrName(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":"IAB1","text":"IAB1 - Arts & Entertainment","id":1},`+
		`{"value":"IAB10","text":"IAB10 - Home & Garden","id":10},`+
		`{"value":"IAB3","text":"IAB3 - Business","id":3},`+
		`{"value":"IAB13","text":"IAB13 - Business Finance","id":13}]}`)

	for search, want := range map[string]string{
		"IAB1":     "1",
		"iab1":     "1",
		"Business": "3",
		"business": "3",
	} {
		got, err := resolveOne(context.Background(), c, iabCategoriesPath, search, "categories")
		if err != nil {
			t.Errorf("%q: %v", search, err)
			continue
		}
		if fmt.Sprint(got) != want {
			t.Errorf("%q resolved to %v, want the id %s", search, got, want)
		}
	}
}

// The code and name rule is the IAB label's alone: elsewhere a " - " is part
// of the name, and only the whole label is an exact match.
func TestResolveOneSplitsOnlyIABLabels(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":7,"text":"Example Coffee - Downtown"},`+
		`{"value":8,"text":"Example Tea - Downtown"}]}`)

	if _, err := resolveOne(context.Background(), c, brandsPath, "Downtown", "brands"); err == nil {
		t.Fatal("a brand was picked by the part of its label after ' - '")
	}
}

// WebDomain reads its categories from iab_category_codes, not categories.
// Writing the wrong field is not rejected by the API, just ignored.
func TestCategoryForNamesBothCatalogAndField(t *testing.T) {
	for dsType, wantKey := range map[string]string{
		"POI":                  "categories",
		"Apps":                 "categories",
		"AffinityTransactions": "categories",
		"WebDomain":            "iab_category_codes",
	} {
		cat, err := categoryFor(dsType)
		if err != nil {
			t.Errorf("categoryFor(%q): %v", dsType, err)
			continue
		}
		if cat.key != wantKey {
			t.Errorf("categoryFor(%q).key = %q, want %q", dsType, cat.key, wantKey)
		}
		if cat.path == "" {
			t.Errorf("categoryFor(%q) has no path", dsType)
		}
	}

	if _, err := categoryFor("CTV"); err == nil {
		t.Fatal("categoryFor accepted a type with no category catalog")
	}
}

// --brand-all takes every match.
func TestResolveAllTakesEveryMatch(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":929,"text":"Dutch Bros Coffee"},{"value":921,"text":"Gregorys Coffee"}]}`)

	got, err := resolveAll(context.Background(), c, brandsPath, "coffee", "brands")
	if err != nil {
		t.Fatalf("resolveAll: %v", err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if string(raw) != "[929,921]" {
		t.Errorf("got %s, want [929,921]", raw)
	}
}

func TestResolveAllRejectsNoMatch(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[]}`)

	if _, err := resolveAll(context.Background(), c, brandsPath, "zzzz", "brands"); err == nil {
		t.Fatal("resolveAll accepted a search with no matches")
	}
}

// A paged catalog is refused, not silently truncated. POI brands do not
// paginate today, so this guards the invariant rather than a live path.
func TestResolveAllRefusesAPagedCatalog(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":{`+
		`"items":[{"value":1,"text":"a"},{"value":2,"text":"b"}],`+
		`"pagination":{"current_page":1,"per_page":2,"total":400,"last_page":200}}}`)

	_, err := resolveAll(context.Background(), c, brandsPath, "wide", "categories")
	if err == nil {
		t.Fatal("resolveAll took page one of a 200-page catalog")
	}
	for _, want := range []string{"400", "200 pages", "narrow"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error omits %q: %v", want, err)
		}
	}
}

// One page is fine even when pagination is reported.
func TestResolveAllAcceptsASinglePage(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":{`+
		`"items":[{"value":23,"text":"Food & Drink"}],`+
		`"pagination":{"current_page":1,"per_page":250,"total":1,"last_page":1}}}`)

	got, err := resolveAll(context.Background(), c, brandsPath, "food", "categories")
	if err != nil {
		t.Fatalf("resolveAll refused a single page: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %d ids, want 1", len(got))
	}
}

func TestAudienceBodyMarshalling(t *testing.T) {

	body := audienceBody{
		Name: "Starbucks visitors - SF - 1 week",
		Datasets: []audienceDataset{{
			Type:            "POI",
			StartDate:       "2026-09-02",
			EndDate:         "2026-09-09",
			Analysisdata:    []any{208},
			SignalProviders: []any{"aaa"},
			Location:        &audienceLocation{Countries: []string{"USA"}, Cities: []string{"San Francisco"}},
		}},
	}

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshalling the body: %v", err)
	}
	got := string(raw)

	for _, want := range []string{
		`"name":"Starbucks visitors - SF - 1 week"`,
		`"type":"POI"`, `"start_date":"2026-09-02"`, `"end_date":"2026-09-09"`,
		`"analysisdata":[208]`, `"signal_providers":["aaa"]`,
		`"countries":["USA"]`, `"cities":["San Francisco"]`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("payload missing %s:\n%s", want, got)
		}
	}
	// One dataset carries no operator; the API stores "Single" itself.
	if strings.Contains(got, "operator") {
		t.Errorf("payload carries an operator for a single dataset:\n%s", got)
	}
	// Unset selectors must not ship as null.
	for _, unwanted := range []string{"categories", "states", "zipcodes"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("payload carries an unset %s:\n%s", unwanted, got)
		}
	}
}

// The search is a substring match, so "Example Coffee" also returns "Example
// Coffee Reserve". One exact label is the one that was meant.
func TestResolveOnePrefersAnExactLabel(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":208,"text":"Example Coffee"},{"value":777,"text":"Example Coffee Reserve"}]}`)

	got, err := resolveOne(context.Background(), c, brandsPath, "example coffee", "brands")
	if err != nil {
		t.Fatalf("resolveOne: %v", err)
	}
	raw, _ := json.Marshal(got)
	if string(raw) != "208" {
		t.Fatalf("resolved %s, want 208 (the exact label, not the longer one)", raw)
	}
}

// Two rows with the same label is a catalog problem, not something to guess at.
func TestResolveOneStillRejectsDuplicateExactLabels(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":1,"text":"Example Fuel"},{"value":2,"text":"Example Fuel"}]}`)

	if _, err := resolveOne(context.Background(), c, brandsPath, "example fuel", "brands"); err == nil {
		t.Fatal("resolveOne picked one of two identically named rows")
	}
}

// An exact label settles it only when every match came back: on one page of
// several, its twin may be on the next.
func TestResolveOneTrustsAnExactLabelOnlyOnACompleteResult(t *testing.T) {
	page := func(total, lastPage int) string {
		return `{"status":"success","code":200,"message":"ok","data":{"items":[` +
			`{"value":1,"text":"Games"},{"value":2,"text":"Games Extra"}],` +
			`"pagination":{"current_page":1,"per_page":2,"total":` + strconv.Itoa(total) +
			`,"last_page":` + strconv.Itoa(lastPage) + `}}}`
	}
	appsCategories := referencePrefix + "apps/categories"

	t.Run("partial page lists", func(t *testing.T) {
		c, _ := catalogServer(t, page(3, 2))
		_, err := resolveOne(context.Background(), c, appsCategories, "games", "categories")
		if err == nil {
			t.Fatal("resolveOne took an exact label from a partial page")
		}
		if !strings.Contains(err.Error(), "showing 2 of 3") {
			t.Errorf("error does not say more matches exist: %v", err)
		}
	})

	t.Run("complete page resolves", func(t *testing.T) {
		c, _ := catalogServer(t, page(2, 1))
		got, err := resolveOne(context.Background(), c, appsCategories, "games", "categories")
		if err != nil {
			t.Fatalf("resolveOne: %v", err)
		}
		if raw, _ := json.Marshal(got); string(raw) != "1" {
			t.Fatalf("resolved %s, want 1 (the exact label)", raw)
		}
	})
}

// No exact label leaves the ambiguity error intact.
func TestResolveOneKeepsAmbiguityWithoutAnExactLabel(t *testing.T) {
	c, _ := catalogServer(t, `{"status":"success","code":200,"message":"ok","data":[`+
		`{"value":929,"text":"Example Coffee North"},{"value":921,"text":"Example Coffee South"}]}`)

	_, err := resolveOne(context.Background(), c, brandsPath, "coffee", "brands")
	if err == nil {
		t.Fatal("resolveOne picked one of several partial matches")
	}
	if !strings.Contains(err.Error(), "narrow the search") {
		t.Errorf("error changed shape: %v", err)
	}
}

// Origin data is weekly: the API widens any window to the whole Monday-to-Sunday
// weeks it touches. 2026-09-02 and 2026-09-09 are both Wednesdays.
func TestOriginWeeks(t *testing.T) {
	for _, tc := range []struct{ start, end, wantStart, wantEnd string }{
		{"2026-09-02", "2026-09-09", "2026-08-31", "2026-09-13"},
		{"2026-08-31", "2026-09-06", "2026-08-31", "2026-09-06"}, // already whole
		{"2026-09-06", "2026-09-06", "2026-08-31", "2026-09-06"}, // a Sunday alone
		{"2026-09-07", "2026-09-07", "2026-09-07", "2026-09-13"}, // a Monday alone
		{"2026-12-30", "2027-01-01", "2026-12-28", "2027-01-03"}, // across the year
	} {
		gotStart, gotEnd := originWeeks(tc.start, tc.end)
		if gotStart != tc.wantStart || gotEnd != tc.wantEnd {
			t.Errorf("originWeeks(%s, %s) = %s, %s; want %s, %s",
				tc.start, tc.end, gotStart, gotEnd, tc.wantStart, tc.wantEnd)
		}
	}
}
