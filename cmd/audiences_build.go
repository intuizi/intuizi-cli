package cmd

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// Catalogs the create flags resolve against, mirroring the equivalent
// 'intuizi reference <group> <item>'.
const (
	brandsPath    = referencePrefix + "poi/brands"
	providersPath = referencePrefix + "common/signal-providers"
)

// categoryCatalog is where --category resolves and where the ids then go.
// Both differ per type: WebDomain keeps its categories under web/iab-categories
// and reads them from iab_category_codes, not categories.
type categoryCatalog struct {
	path string
	key  string
}

var categoryCatalogs = map[string]categoryCatalog{
	"POI":                  {referencePrefix + "poi/categories", "categories"},
	"Apps":                 {referencePrefix + "apps/categories", "categories"},
	"AffinityTransactions": {referencePrefix + "affinity-transactions/categories", "categories"},
	"WebDomain":            {referencePrefix + "web/iab-categories", "iab_category_codes"},
}

// categoryFor rejects types with no category catalog, rather than sending a
// field the API would ignore.
func categoryFor(dsType string) (categoryCatalog, error) {
	if c, ok := categoryCatalogs[dsType]; ok {
		return c, nil
	}
	valid := make([]string, 0, len(categoryCatalogs))
	for k := range categoryCatalogs {
		valid = append(valid, k)
	}
	sort.Strings(valid)
	return categoryCatalog{}, usageErr("--category applies to " +
		strings.Join(valid, ", ") + ", not " + dsType)
}

// resolveOne insists on exactly one match. A guessed id builds an audience
// that completes with no devices and no error. ReadList takes both catalog
// shapes, so nothing here switches on that.
func resolveOne(ctx context.Context, c *api.Client, path, search, what string) (any, error) {
	query := url.Values{}
	query.Set("search", search)

	items, pg, err := api.ReadList[output.Record](ctx, c, path, query)
	if err != nil {
		return nil, err
	}

	switch len(items) {
	case 1:
		return catalogValue(items[0], path)
	case 0:
		return nil, usageErr(fmt.Sprintf("no %s match %q", what, search))
	default:
		// The search is a substring match, so "Example Coffee" also returns
		// "Example Coffee Reserve". One exact label is the one that was meant.
		if exact := exactMatches(items, search); len(exact) == 1 {
			return catalogValue(exact[0], path)
		}
		// Ids too: the fix is usually to pass one.
		var b strings.Builder
		if pg != nil && pg.Total > len(items) {
			// A paged catalog answered one page; the rest are not listed.
			fmt.Fprintf(&b, "%d %s match %q - showing %d of %d matches, narrow the search or pass an id:",
				pg.Total, what, search, len(items), pg.Total)
		} else {
			fmt.Fprintf(&b, "%d %s match %q - narrow the search, or pass an id:",
				len(items), what, search)
		}
		for _, it := range items {
			fmt.Fprintf(&b, "\n  %-8v %v", catalogID(it), catalogLabel(it))
		}
		return nil, usageErr(b.String())
	}
}

// exactMatches returns rows whose label equals the search, ignoring case and
// space. Duplicates return all hits, so the caller still lists them.
func exactMatches(items []output.Record, search string) []output.Record {
	want := strings.ToLower(strings.TrimSpace(search))
	var hits []output.Record
	for _, it := range items {
		if label, ok := catalogLabel(it).(string); ok &&
			strings.ToLower(strings.TrimSpace(label)) == want {
			hits = append(hits, it)
		}
	}
	return hits
}

// catalogID is catalogValue for a listing, where a row with no id is still
// worth showing rather than aborting the message.
func catalogID(r output.Record) any {
	v, err := catalogValue(r, "")
	if err != nil {
		return "?"
	}
	return v
}

// catalogLabel reads a row's label from either catalog shape.
func catalogLabel(r output.Record) any {
	for _, k := range []string{"text", "name"} {
		if v := r[k]; v != nil {
			return v
		}
	}
	return "?"
}

// catalogValue reads a row's id. Catalogs answer with value/text or id/name,
// and a missing one would marshal as null - which the API ignores, leaving an
// audience that completes with no devices.
func catalogValue(r output.Record, path string) (any, error) {
	for _, k := range []string{"value", "id"} {
		if v := r[k]; v != nil {
			return v, nil
		}
	}
	return nil, fmt.Errorf("a row from %s carries no value or id", path)
}

// resolveOrID takes a name or the id itself; names are never purely numeric.
// No catalog filters by id, so an id is only checked for sign: zero and
// negatives stop here, like parseID, rather than building an empty audience.
// flag is the flag the value came from, for the error.
func resolveOrID(ctx context.Context, c *api.Client, path, flag, value, what string) (any, error) {
	if id, err := strconv.Atoi(value); err == nil {
		if id <= 0 {
			return nil, usageErr(fmt.Sprintf("%s %s is not a valid id; ids are positive, or pass a name",
				flag, value))
		}
		return id, nil
	}
	return resolveOne(ctx, c, path, value, what)
}

// allProviders backs the --provider default. A provider left out is the
// quietest way to get an empty audience. Sets differ per type, so no cache.
func allProviders(ctx context.Context, c *api.Client, dataType string) ([]any, error) {
	query := url.Values{}
	query.Set("dataType", dataType)

	items, _, err := api.ReadList[output.Record](ctx, c, providersPath, query)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("no signal providers for dataset type %q", dataType)
	}

	values := make([]any, 0, len(items))
	for _, it := range items {
		v, err := catalogValue(it, providersPath)
		if err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, nil
}

// checkProviders keeps --provider to the type's catalog, in the order given.
// The API does not reject an id outside it: the audience completes with zero
// devices, which is what the help warns about, and a typo is the likeliest
// way there.
func checkProviders(given []string, catalog []any, dataType string) ([]any, error) {
	valid := make([]string, 0, len(catalog))
	known := make(map[string]bool, len(catalog))
	for _, v := range catalog {
		// Compared as text: catalog values decode as json.Number or string.
		s := fmt.Sprint(v)
		valid = append(valid, s)
		known[s] = true
	}
	out := make([]any, 0, len(given))
	for _, p := range given {
		if !known[p] {
			return nil, usageErr(fmt.Sprintf("--provider %s is not a signal provider for %s; one of %s",
				p, dataType, strings.Join(valid, ", ")))
		}
		out = append(out, p)
	}
	return out, nil
}

// datasetTypes maps a lowercased type to the API's spelling, which varies and
// is not guessable. Hardcoded because dataset-types comes back empty on
// unprovisioned accounts.
var datasetTypes = map[string]string{
	"poi":                  "POI",
	"webdomain":            "WebDomain",
	"ctv":                  "CTV",
	"apps":                 "Apps",
	"cohorts":              "Cohorts",
	"affinitytransactions": "AffinityTransactions",
	"demographics":         "Demographics",
	"deidentified":         "Deidentified",
	"profileattributes":    "ProfileAttributes",
}

// canonicalType makes --type case-insensitive and rejects unknown ones.
func canonicalType(t string) (string, error) {
	if canon, ok := datasetTypes[strings.ToLower(t)]; ok {
		return canon, nil
	}
	valid := make([]string, 0, len(datasetTypes))
	for _, v := range datasetTypes {
		valid = append(valid, v)
	}
	sort.Strings(valid)
	return "", usageErr("unknown --type " + t + "; one of " + strings.Join(valid, ", "))
}

// resolveAll backs --brand-all: every match, where resolveOne insists on one.
// A multi-page catalog is refused rather than quietly taking the first page.
func resolveAll(ctx context.Context, c *api.Client, path, search, what string) ([]any, error) {
	query := url.Values{}
	query.Set("search", search)

	items, pg, err := api.ReadList[output.Record](ctx, c, path, query)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, usageErr(fmt.Sprintf("no %s match %q", what, search))
	}
	if pg != nil && pg.LastPage > 1 {
		return nil, usageErr(fmt.Sprintf(
			"%q matches %d %s across %d pages - narrow the search, or pass ids",
			search, pg.Total, what, pg.LastPage))
	}

	ids := make([]any, 0, len(items))
	for _, it := range items {
		v, err := catalogValue(it, path)
		if err != nil {
			return nil, err
		}
		ids = append(ids, v)
	}
	return ids, nil
}

// audienceLocation omits dmas, which the API rejects.
type audienceLocation struct {
	Countries []string `json:"countries,omitempty"`
	States    []string `json:"states,omitempty"`
	Cities    []string `json:"cities,omitempty"`
	Zipcodes  []string `json:"zipcodes,omitempty"`
}

// audienceDataset is typed, not a map, so a misspelled key is a compile error
// - the one mistake --file cannot catch. analysisdata is POI's brand selector;
// AffinityTransactions calls the same thing "brands".
type audienceDataset struct {
	Type             string            `json:"type"`
	StartDate        string            `json:"start_date"`
	EndDate          string            `json:"end_date"`
	Analysisdata     []any             `json:"analysisdata,omitempty"`
	Categories       []any             `json:"categories,omitempty"`
	IABCategoryCodes []any             `json:"iab_category_codes,omitempty"`
	SignalProviders  []any             `json:"signal_providers,omitempty"`
	Location         *audienceLocation `json:"location,omitempty"`
}

// audienceBody is the single-dataset payload; two datasets go through --file.
// No operator: the API stores "Single" itself, and it is not a valid input.
type audienceBody struct {
	Name     string            `json:"name"`
	Datasets []audienceDataset `json:"datasets"`
}

// dateLayout is the payload's format. The summary "dataset" field renders
// MM/DD/YYYY on read; normalized_payload echoes this one.
const dateLayout = "2006-01-02"

// parseWindow catches a transposed pair, which otherwise builds an empty
// audience with nothing to explain why.
func parseWindow(start, end string) error {
	s, err := time.Parse(dateLayout, start)
	if err != nil {
		return usageErr("--start-date must be YYYY-MM-DD, not " + start)
	}
	e, err := time.Parse(dateLayout, end)
	if err != nil {
		return usageErr("--end-date must be YYYY-MM-DD, not " + end)
	}
	if e.Before(s) {
		return usageErr(fmt.Sprintf("--end-date %s is before --start-date %s", end, start))
	}
	return nil
}
