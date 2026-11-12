package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// The SKUs dataset: online purchases at product level, by merchant and
// product brand. Contracts: docs-src/content/api/v2/audiences.md (SKUs
// fields) and docs-src/content/api/v2/reference/skus.md in intuizi-console.

// The two catalogs a SKUs dataset names keys from. Merchants is one flat
// list; brands is paged, and searched by name.
const (
	skusMerchantsPath = referencePrefix + "skus/merchants"
	skusBrandsPath    = referencePrefix + "skus/brands"
)

// Match Strictness, the API's max_devices_per_ip on a SKUs dataset: 1 Very
// Strict to 5 More Reach. The API's default is 3.
const (
	minMatchStrictness = 1
	maxMatchStrictness = 5
)

// matchStrictnessLevels are the values --match-strictness takes, for
// completion.
var matchStrictnessLevels = []string{"1", "2", "3", "4", "5"}

// maxSearchPage is the largest page a catalog read serves. The SKUs brand
// catalog pages 100 rows by default, too few for a search such as "dove" to
// arrive whole.
const maxSearchPage = 500

// skusFlags are the flags that write a SKUs dataset's own keys, refused on any
// other type. --brand and --brand-all are shared with POI.
var skusFlags = []string{"merchant", "match-strictness"}

// skusSpec is what the SKUs flags ask for, checked before any key is looked
// up.
type skusSpec struct {
	merchants  []string
	brands     []string
	brandAll   []string
	strictness int // 0: not given, so not sent
}

// skusOptions checks the SKUs flags. On any other type the SKUs-only flags are
// refused, as --filter is outside POI; on SKUs the flags it does not read are.
// Nothing here needs a token.
func (r *audienceRequest) skusOptions(dsType string, changed func(string) bool) (skusSpec, error) {
	if dsType != "SKUs" {
		for _, f := range skusFlags {
			if changed(f) {
				msg := "--" + f + " applies to --type SKUs, not " + dsType
				if f == "match-strictness" && (dsType == "WebDomain" || dsType == "AffinityTransactions") {
					msg += "; a " + dsType + " max_devices_per_ip needs --file"
				}
				return skusSpec{}, usageErr(msg)
			}
		}
		return skusSpec{}, nil
	}

	// The dataset has one source and takes no provider list, and its
	// geography is countries and states only: the API refuses the rest.
	if changed("provider") {
		return skusSpec{}, usageErr("--provider does not apply to --type SKUs: the dataset takes no signal providers")
	}
	for _, f := range []string{"city", "zipcode"} {
		if changed(f) {
			return skusSpec{}, usageErr("--" + f + " does not apply to --type SKUs: it reads --country and --state only")
		}
	}
	if err := nonEmpty("merchant", r.merchants...); err != nil {
		return skusSpec{}, err
	}
	if len(r.merchants)+len(r.brands)+len(r.brandAll) == 0 {
		return skusSpec{}, usageErr("--type SKUs needs --merchant or --brand, or both; find the keys with " +
			"'intuizi reference skus merchants' and 'intuizi reference skus brands --search <name>'")
	}
	if changed("match-strictness") && (r.strictness < minMatchStrictness || r.strictness > maxMatchStrictness) {
		return skusSpec{}, usageErr(fmt.Sprintf("--match-strictness must be between %d and %d, not %d",
			minMatchStrictness, maxMatchStrictness, r.strictness))
	}
	return skusSpec{merchants: r.merchants, brands: r.brands, brandAll: r.brandAll, strictness: r.strictness}, nil
}

// searchValues is the query a name lookup sends: the search, plus the largest
// page on the SKUs brand catalog.
func searchValues(path, search string) url.Values {
	query := url.Values{}
	query.Set("search", search)
	if path == skusBrandsPath {
		query.Set("per_page", strconv.Itoa(maxSearchPage))
	}
	return query
}

// resolveSkusKeys looks up every --merchant or --brand value of a SKUs dataset
// and returns the catalog keys, in the order given, each once: the API refuses
// a key named twice.
func resolveSkusKeys(ctx context.Context, c *api.Client, path, flag string, values []string, what string) ([]string, error) {
	keys := make([]string, 0, len(values))
	for _, v := range values {
		key, err := resolveSkusKey(ctx, c, path, flag, v, what)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return dedupe(keys), nil
}

// resolveSkusKey finds the catalog key a --merchant or --brand value names.
// The API matches a key exactly, case included, and refuses the name, so the
// value is looked up rather than sent as typed. A row whose key is the value
// wins at once: keys are unique, so no later page can hold a twin. Otherwise
// one match, or exactly one row labelled with the value (ignoring case) among
// every match, is taken, as for a POI brand. The search matches the name, not
// the key, so a key that does not contain its own name is not found here:
// such a key goes in a --file body.
func resolveSkusKey(ctx context.Context, c *api.Client, path, flag, value, what string) (string, error) {
	want := strings.TrimSpace(value)
	items, pg, err := api.ReadList[output.Record](ctx, c, path, searchValues(path, want))
	if err != nil {
		return "", err
	}

	lookup := "'intuizi reference skus " + what + "'"
	if path == skusBrandsPath {
		lookup = "'intuizi reference skus brands --search <name>'"
	}

	for _, it := range items {
		if key, ok := it["value"].(string); ok && key == want {
			return key, nil
		}
	}
	partial := pg != nil && pg.Total > len(items)
	pick := func(r output.Record) (string, error) {
		key, ok := r["value"].(string)
		if !ok || key == "" {
			return "", fmt.Errorf("a row from %s carries no key in its value", path)
		}
		return key, nil
	}

	switch {
	case len(items) == 0:
		return "", usageErr(fmt.Sprintf("%s %s: no SKUs %s match it; list them with %s", flag, value, what, lookup))
	case len(items) == 1 && !partial:
		return pick(items[0])
	}
	if exact := exactMatches(items, want, path); len(exact) == 1 && !partial {
		return pick(exact[0])
	}

	var b strings.Builder
	if partial {
		fmt.Fprintf(&b, "%s %s: %d SKUs %s match it - showing %d, narrow the search or pass the key from the value column:",
			flag, value, pg.Total, what, len(items))
	} else {
		fmt.Fprintf(&b, "%s %s: %d SKUs %s match it - narrow the search, or pass the key from the value column:",
			flag, value, len(items), what)
	}
	for _, it := range items {
		fmt.Fprintf(&b, "\n  %-30v %v", catalogID(it, path), catalogLabel(it))
	}
	return "", usageErr(b.String())
}

// skusAll backs --brand-all on a SKUs dataset: every brand key a search
// matches, as strings.
func skusAll(ctx context.Context, c *api.Client, search string) ([]string, error) {
	values, err := resolveAll(ctx, c, skusBrandsPath, search, "SKUs brands")
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for _, v := range values {
		key, ok := v.(string)
		if !ok || key == "" {
			return nil, fmt.Errorf("a row from %s carries no key in its value", skusBrandsPath)
		}
		keys = append(keys, key)
	}
	return keys, nil
}
