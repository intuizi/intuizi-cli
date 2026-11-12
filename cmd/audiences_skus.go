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

// The catalogs a SKUs dataset names keys from. Merchants is one flat list;
// brands is paged, and searched by name. Categories is a tree (the Google
// Product Taxonomy with finer levels) whose search reads every level, at most
// 100 rows and not paged. Device values (skusDeviceValuesPath) come per field.
const (
	skusMerchantsPath  = referencePrefix + "skus/merchants"
	skusBrandsPath     = referencePrefix + "skus/brands"
	skusCategoriesPath = referencePrefix + "skus/categories"
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
// other type and next to --file (audienceFields takes them from here). The
// device flags come from skusDeviceFlags, so a new one cannot be left out.
// --brand, --brand-all and --category are shared with other types.
var skusFlags = func() []string {
	flags := []string{"merchant", "match-strictness"}
	for _, d := range skusDeviceFlags {
		flags = append(flags, d.flag)
	}
	return flags
}()

// skusDevice is one "Purchased on" flag: the flag, the device-values field it reads and its help noun.
type skusDevice struct {
	flag  string
	field string
	help  string
}

var skusDeviceFlags = []skusDevice{
	{"device-type", "type", "device type"},
	{"device-os", "os", "OS"},
	{"device-make", "make", "device make"},
	{"device-model", "model", "device model"},
}

const skusDeviceValuesPath = referencePrefix + "skus/device-values"

// skusDeviceValuesCap is the most rows the device-values catalog answers a
// search with, the most orders first. It is not paged, so a full answer may
// have cut off the value searched for.
const skusDeviceValuesCap = 500

// resolveSkusDevices sends each value of one device flag as the catalog holds it: the API matches exactly (upper
// case), the CLI matches ignoring case. A value the catalog does not hold is a usage error naming it. skusOptions
// has already refused an empty value.
func resolveSkusDevices(ctx context.Context, c *api.Client, d skusDevice, values []string) ([]string, error) {
	out := []string{}
	for _, v := range values {
		want := strings.TrimSpace(v)
		query := url.Values{}
		query.Set("field", d.field)
		query.Set("search", want)
		items, _, err := api.ReadList[output.Record](ctx, c, skusDeviceValuesPath, query)
		if err != nil {
			return nil, err
		}
		found := ""
		for _, it := range items {
			if s, ok := it["value"].(string); ok && strings.EqualFold(s, want) {
				found = s
				break
			}
		}
		if found == "" {
			lookup := "'intuizi reference skus device-values --field " + d.field + "'"
			switch {
			case len(items) == 0:
				// An account without the dataset reads every SKUs catalog
				// empty, so "no match" can mean either.
				return nil, usageErr(fmt.Sprintf("--%s %s: no SKUs %s value matches it; list them with %s. If "+
					"that lists nothing at all, the SKUs dataset is not enabled for this account: 'intuizi "+
					"reference common dataset-types' lists the types it has", d.flag, v, d.help, lookup))
			case len(items) >= skusDeviceValuesCap:
				return nil, usageErr(fmt.Sprintf("--%s %s: the search was cut short at %d SKUs %s values, the "+
					"most orders first, and %s is not among them, so it cannot be checked; pass a longer value, "+
					"or send it as the catalog holds it (upper case) in a --file body", d.flag, v,
					skusDeviceValuesCap, d.help, v))
			}
			return nil, usageErr(fmt.Sprintf("--%s %s: not a value of the SKUs catalog; list them with %s",
				d.flag, v, lookup))
		}
		out = append(out, found)
	}
	return dedupe(out), nil
}

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
	// An empty value would otherwise drop out, and an unset shell variable
	// would post an audience of every purchase device.
	for _, d := range skusDeviceFlags {
		if err := nonEmpty(d.flag, *r.devices[d.flag]...); err != nil {
			return skusSpec{}, err
		}
	}
	if len(r.merchants)+len(r.brands)+len(r.brandAll)+len(r.categories) == 0 {
		return skusSpec{}, usageErr("--type SKUs needs --merchant, --brand or --category; find them with " +
			"'intuizi reference skus merchants', 'intuizi reference skus brands --search <name>' and " +
			"'intuizi reference skus categories --search <name>'")
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
		// An account without the dataset reads every SKUs catalog empty, so
		// "no match" can mean either.
		return "", usageErr(fmt.Sprintf("%s %s: no SKUs %s match it; list them with %s. If that lists "+
			"nothing at all, the SKUs dataset is not enabled for this account: 'intuizi reference common "+
			"dataset-types' lists the types it has", flag, value, what, lookup))
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
