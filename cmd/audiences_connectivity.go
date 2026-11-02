package cmd

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// The Connectivity dataset and the Network Switching analysis. Contracts:
// docs-src/content/api/v2/audiences.md (Connectivity fields, Network
// Switching) and docs-src/content/api/v2/reference/connectivity.md in
// intuizi-console.

// networkKinds are the values of --network-kind and --switch-network: the home
// internet network of a device, or its mobile carrier. The API takes them in
// lower case only.
var networkKinds = []string{"home", "mobile"}

// connectivityTechnologies are the values --technology takes, in the API's own
// spelling. They describe home networks only.
var connectivityTechnologies = []string{"Fiber", "Cable", "DSL", "Cable or DSL", "Satellite", "Fixed wireless"}

// errSwitchingType is the API's own rule, refused before anything is sent.
const errSwitchingType = "Network switching runs on an audience with one Connectivity dataset."

// Bounds the API sets, checked before anything is sent.
const (
	minConnectivityDays = 1
	maxConnectivityDays = 7
	minHomeDaysLow      = 2
	minHomeDaysHigh     = 5
	defaultMinHomeDays  = 3
	monthLayout         = "2006-01"
)

// connectivityFlags are the flags that write a Connectivity dataset's own
// keys, refused on any other type.
var connectivityFlags = []string{"network-kind", "network", "technology", "min-days"}

// switchingFlags are the flags that write the networkswitching block.
var switchingFlags = []string{"switch-from", "switch-to", "switch-network", "switch-min-days"}

// canonicalKind lower-cases a --network-kind or --switch-network value and
// rejects one the API does not take.
func canonicalKind(flag, v string) (string, error) {
	kind := strings.ToLower(strings.TrimSpace(v))
	if !slices.Contains(networkKinds, kind) {
		return "", usageErr("unknown --" + flag + " " + v + "; one of " + strings.Join(networkKinds, ", "))
	}
	return kind, nil
}

// canonicalTechnologies makes --technology case-insensitive, in the API's
// spelling, and drops a repeat.
func canonicalTechnologies(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, v := range values {
		want := strings.ToLower(strings.Join(strings.Fields(v), " "))
		i := slices.IndexFunc(connectivityTechnologies, func(t string) bool { return strings.ToLower(t) == want })
		if i < 0 {
			return nil, usageErr("unknown --technology " + v + "; one of " + strings.Join(connectivityTechnologies, ", "))
		}
		out = append(out, connectivityTechnologies[i])
	}
	return dedupe(out), nil
}

// connectivitySpec is what the Connectivity flags ask for, checked before any
// network name is looked up.
type connectivitySpec struct {
	kind         string
	networks     []string
	technologies []string
	minDays      int // 0: not given, so not sent
}

// connectivityOptions checks the Connectivity flags. On any other type each
// one is refused, as --filter is outside POI. Nothing here needs a token.
func (r *audienceRequest) connectivityOptions(dsType string, changed func(string) bool) (connectivitySpec, error) {
	if dsType != "Connectivity" {
		for _, f := range connectivityFlags {
			if changed(f) {
				return connectivitySpec{}, usageErr("--" + f + " applies to --type Connectivity, not " + dsType)
			}
		}
		return connectivitySpec{}, nil
	}

	if !changed("network-kind") {
		return connectivitySpec{}, usageErr("--type Connectivity needs --network-kind: home for home internet " +
			"networks, or mobile for mobile carriers")
	}
	kind, err := canonicalKind("network-kind", r.networkKind)
	if err != nil {
		return connectivitySpec{}, err
	}
	for _, v := range []struct {
		flag   string
		values []string
	}{{"network", r.networks}, {"technology", r.technologies}} {
		if err := nonEmpty(v.flag, v.values...); err != nil {
			return connectivitySpec{}, err
		}
	}
	techs, err := canonicalTechnologies(r.technologies)
	if err != nil {
		return connectivitySpec{}, err
	}
	if len(techs) > 0 && kind == "mobile" {
		return connectivitySpec{}, usageErr("--technology applies to home networks only, not --network-kind mobile")
	}
	if changed("min-days") && (r.minDays < minConnectivityDays || r.minDays > maxConnectivityDays) {
		return connectivitySpec{}, usageErr(fmt.Sprintf("--min-days must be between %d and %d, not %d",
			minConnectivityDays, maxConnectivityDays, r.minDays))
	}
	// The API needs a country, and checks it is one with Connectivity data.
	if len(r.countries) == 0 {
		return connectivitySpec{}, usageErr("--type Connectivity needs --country; list the covered countries with " +
			"'intuizi reference common countries --dataset-type Connectivity'")
	}
	return connectivitySpec{kind: kind, networks: r.networks, technologies: techs, minDays: r.minDays}, nil
}

// switchingOptions checks the Network Switching flags and returns the block
// they write, or nil when none was given. Nothing here needs a token.
func (r *audienceRequest) switchingOptions(dsType string, changed func(string) bool) (*networkSwitching, error) {
	asked := false
	for _, f := range switchingFlags {
		asked = asked || changed(f)
	}
	if !asked {
		return nil, nil
	}
	if !changed("switch-from") || !changed("switch-to") {
		return nil, usageErr("--switch-from and --switch-to go together: network switching compares " +
			"the months they name")
	}
	if dsType != "Connectivity" {
		return nil, usageErr(errSwitchingType)
	}
	from, err := parseMonth("switch-from", r.switchFrom)
	if err != nil {
		return nil, err
	}
	to, err := parseMonth("switch-to", r.switchTo)
	if err != nil {
		return nil, err
	}
	if !from.Before(to) {
		return nil, usageErr(fmt.Sprintf("--switch-from %s must be before --switch-to %s", r.switchFrom, r.switchTo))
	}
	network, err := canonicalKind("switch-network", r.switchNetwork)
	if err != nil {
		return nil, err
	}
	if r.switchMinDays < minHomeDaysLow || r.switchMinDays > minHomeDaysHigh {
		return nil, usageErr(fmt.Sprintf("--switch-min-days must be between %d and %d, not %d",
			minHomeDaysLow, minHomeDaysHigh, r.switchMinDays))
	}
	return &networkSwitching{
		NetworkType: network,
		FromMonth:   from.Format(monthLayout),
		ToMonth:     to.Format(monthLayout),
		MinHomeDays: r.switchMinDays,
	}, nil
}

// parseMonth reads a YYYY-MM month.
func parseMonth(flag, v string) (time.Time, error) {
	m, err := time.Parse(monthLayout, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, usageErr("--" + flag + " must be YYYY-MM, not " + v)
	}
	return m, nil
}

// weeklyNote names the window a weekly dataset actually scans, when the API
// widens the dates given, or "" when there is nothing to say. The body keeps
// the dates as given: the API does the widening.
func weeklyNote(dsType, start, end string) string {
	if dsType != "Origin" && dsType != "Connectivity" {
		return ""
	}
	from, to := originWeeks(start, end)
	if from == start && to == end {
		return ""
	}
	note := fmt.Sprintf("%s data is weekly: the API widens %s..%s to the whole weeks %s..%s",
		dsType, start, end, from, to)
	if dsType == "Connectivity" {
		// The stored window runs from the first to the last built week.
		note += ", and keeps the weeks that are built"
	}
	return note
}

// maxCatalogPages bounds the pages read to check --network. The catalog holds
// curated brands only, so it fits in one page of 500 today.
const maxCatalogPages = 20

// connectivityNetworks reads every network of kind from the catalog, page by
// page: a name on a later page is still a name the API accepts.
func connectivityNetworks(ctx context.Context, c *api.Client, kind string) ([]output.Record, error) {
	var all []output.Record
	for page := 1; page <= maxCatalogPages; page++ {
		query := url.Values{}
		query.Set("kind", kind)
		query.Set("per_page", "500")
		if page > 1 {
			query.Set("page", strconv.Itoa(page))
		}
		items, pg, err := api.ReadList[output.Record](ctx, c, networksPath, query)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if pg == nil || pg.LastPage <= page || len(items) == 0 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("the %s network catalog runs past %d pages", kind, maxCatalogPages)
}

// maxListedNetworks is how many names a refused --network lists; past it the
// error points at the reference read instead.
const maxListedNetworks = 30

// checkNetworks keeps --network to the catalog of the kind, in the order
// given, ignoring case and spacing, and sends the catalog's own spelling. The
// API refuses a name outside it anyway; this says so before anything is sent.
func checkNetworks(given []string, catalog []output.Record, kind string) ([]string, error) {
	lookup := fmt.Sprintf("'intuizi reference connectivity networks --kind %s'", kind)
	if len(catalog) == 0 {
		return nil, fmt.Errorf("the Connectivity catalog lists no %s networks: Connectivity data is not "+
			"available yet", kind)
	}
	fold := func(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }
	known := make(map[string]string, 2*len(catalog))
	valid := make([]string, 0, len(catalog))
	for _, row := range catalog {
		value, _ := row["value"].(string)
		if value == "" {
			continue
		}
		valid = append(valid, value)
		known[fold(value)] = value
		if text, ok := row["text"].(string); ok && text != "" {
			if _, taken := known[fold(text)]; !taken {
				known[fold(text)] = value
			}
		}
	}
	out := make([]string, 0, len(given))
	for _, name := range given {
		value, ok := known[fold(name)]
		if !ok {
			msg := fmt.Sprintf("--network %s is not a %s network in the Connectivity catalog; ", name, kind)
			if len(valid) <= maxListedNetworks {
				msg += "one of " + strings.Join(valid, ", ") + ", or "
			}
			msg += "leave out --network to read any " + kind + " network. List them with " + lookup
			return nil, usageErr(msg)
		}
		out = append(out, value)
	}
	return dedupe(out), nil
}
