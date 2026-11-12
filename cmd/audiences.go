package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/spf13/cobra"
)

// audiencesPrefix is the path every audience call hangs off.
const audiencesPrefix = "/analyses/audiences"

// audienceColumns lead both the list table and the show view. The index returns
// thirteen fields per audience; --json prints all of them.
var audienceColumns = []string{"id", "name", "status", "results_count", "created_at"}

// audienceLifecycle is how --wait follows an audience build. The record a wait
// ends on is followed by the Network Switching summary, as 'show' prints it.
var audienceLifecycle = func() lifecycle {
	lc := resourceLifecycle("audience", audiencesPrefix, audienceColumns)
	lc.after = printNetworkSwitching
	return lc
}()

var audiencesCmd = &cobra.Command{
	Use:   "audiences",
	Short: "Manage audiences",
	Long: `Manage audiences.

An audience defines a group of devices drawn from one or two datasets - places
visited, apps used, CTV viewing, web activity, the networks devices use.
Delivering one somewhere is a separate step: see 'intuizi activations'.

Building is asynchronous. 'audiences create' returns as soon as the audience is
queued, in an Initiating state; add --wait to 'create' or 'show' to block until
it reads Completed. An audience that is still building is not a failure.

'audiences estimate create' takes the same flags or file and reports how many
devices the audience would hold, without creating it. In an organization
billed in credits, 'audiences quote' takes them too and prices the build.

The ids and codes a payload is built from come from 'intuizi reference'.`,
}

// --------------------------------------------------------------------------------- create

func audiencesCreateCommand() *cobra.Command {
	var req audienceRequest

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an audience, from flags or a payload file",
		Long: `Create an audience.

A single-dataset audience can be built from flags for any type whose required
fields all have a flag, with names resolved against the reference catalogs
rather than pasted as ids:

    intuizi audiences create \
      --type POI --brand starbucks \
      --country USA --state CA --city "San Francisco" \
      --start-date 2026-09-02 --end-date 2026-09-09 \
      --name "Starbucks visitors - SF - 1 week"

--brand and --category both take a name or an id. A name is a contains match:
when several entries come back and exactly one is labelled with the name
itself, ignoring case, that one is taken, so "Example Coffee" resolves even
though "Example Coffee Reserve" also matches. Otherwise no match or several is
an error listing what was found, and so is an exact label on a result too long
to arrive in one page. --brand-all takes every match for a search instead, for
the deliberate "all the coffee brands" case, and reports to stderr how many it
selected. --brand applies to POI and SKUs (see --type skus below). --category
applies to POI, Apps, WebDomain and AffinityTransactions, each resolving
against its own catalog. For
WebDomain that is 'intuizi reference web iab-categories', whose id is the
number in its id column, not the IAB code in its value column. Its labels read
"CODE - name", so there an exact IAB code or an exact name picks its row too:
IAB1 resolves to IAB1, not to a list of IAB1 to IAB19. Every selector repeats
for more than one value.

Most types also need --country: the API rejects a dataset without one on
every type except Cohorts, AffinityTransactions, ProfileAttributes and SKUs.
The CLI checks for it before sending only on Origin and Connectivity.

--type origin targets devices by their home location rather than the places
they visited, so its only filters are geographic. --country is required, and
the countries with Origin data come from 'intuizi reference common countries
--dataset-type Origin'; --state, --city and --zipcode narrow it further, and
--brand and --category do not apply. Origin data is weekly: the API widens the
window to the whole Monday-to-Sunday weeks it touches, and the CLI reports the
widened dates on stderr when they differ from the ones given.

--type connectivity targets devices by the network they used: the home
internet network, or the mobile carrier, a device was on for most days of a
week. --network-kind is required: home or mobile. --network narrows it to
named networks, each checked against 'intuizi reference connectivity networks
--kind <kind>' before anything is sent, and --technology narrows home networks
to Fiber, Cable, DSL, Cable or DSL, Satellite or Fixed wireless. Leave both
out to read any network of the kind, including networks the catalog does not
list. --min-days is the fewest days of a week a device must have used the
network, 1 to 7 (the API's default is 3). --country is required, and --state,
--city and --zipcode narrow it to the devices whose home in the same week is in
that area. Connectivity data is weekly too: the API widens the window to whole
weeks and keeps the weeks that are built, and the CLI reports the widened dates
on stderr. These four flags are refused on any other type.

--switch-from and --switch-to run the Network Switching analysis on the built
audience. For each device it finds the home internet network, or with
--switch-network mobile the mobile carrier, in each of the two months, and
counts the devices that kept their network, changed it and switched. It runs
on an audience of one Connectivity dataset only, with --type connectivity.
Each month is read as its last full Monday-to-Sunday week, and both weeks must
be built Connectivity weeks. --switch-min-days (2 to 5, default 3) is the
fewest days of that week a device must be seen on a network for it to be its
home network. The dataset keeps its own dates.

    intuizi audiences create --type connectivity --network-kind home \
      --network Starlink --country USA \
      --start-date 2026-10-19 --end-date 2026-10-25 \
      --switch-from 2026-09 --switch-to 2026-10 \
      --name "Starlink switching - September to October 2026"

'intuizi audiences show <id>' prints the results once the audience is
Completed. The analysis requires additional permissions which need to be
approved by your Account Manager: a 403 means they are not enabled for the
account. Only 'audiences create' takes these flags, because an estimate and a
quote never run the analysis.

--type skus targets the devices of the households that bought a product
brand, or bought from a merchant, online. The data is online purchases at
product level from a panel of affiliate-tracked orders, strongest at large
United States retailers, and the devices are those of the household the
purchase click came from: an order with no tracked click adds no device. The
dataset is available on request. It needs --merchant or --brand, or both. The
values of one flag are alternatives, and the two narrow each other: a brand
with a merchant is that brand bought from that merchant. Each value is a
catalog key or a name, looked up in 'intuizi reference skus merchants' or
'intuizi reference skus brands --search <name>' and sent as the key the
catalog returns, each once. --brand-all takes every brand a search matches.
--match-strictness sets how strictly a device must be tied to the household
of the purchase click, from 1 (Very Strict, the fewest devices) to 5 (More
Reach, every device linked to the household); the API's default is 3. The
dates are the purchase dates. --country and --state are optional: without a
country the purchases of every country are read. SKUs takes no signal
providers, so no provider catalog is read, and --provider, --city and
--zipcode are refused before anything is sent:

    intuizi audiences create --type skus \
      --merchant "Example Outdoor Store" --brand dove \
      --country USA --match-strictness 3 \
      --start-date 2026-10-01 --end-date 2026-10-31 \
      --name "Dove buyers - October 2026"

A SKUs dataset cannot be combined with a Cohorts, Demographics, Origin,
Connectivity or Deidentified dataset, and an audience with a SKUs dataset
takes no additional analyses (analyses, crossvisitation, crosspurchase) and
no refine. The flags build none of these; do not send them with --file.

Omitting --provider includes every signal provider for the dataset type, which
is almost always what you want. The CLI reads that catalog first, and a
--provider it does not list is rejected before anything is created, since the
API would accept it and build an audience that completes with zero devices.

--dry-run prints the body those flags produce and creates nothing. It still
resolves names and reads the signal-provider catalog, so it needs a token. It
doubles as a starting point for the file form:

    intuizi audiences create --type POI ... --dry-run > audience.json

Two datasets need an operator, and refine, crossvisitation and crosspurchase
are nested, so those are passed whole instead. So are the Cohorts,
Demographics and ProfileAttributes types, which the flags refuse before
anything is sent: they require fields no flag writes (a cohort_id, a
demographic filter, profile_attributes rows or profile_attribute_groups), and
Demographics also rejects the dates and signal providers the flags always
send. The same goes for any other field no flag writes, such as project_id,
POI locations, DMAs, the day-part frequency analysis and datastreams. The
file is forwarded untouched, so a field this CLI has never heard of still
reaches the API:

    intuizi audiences create --file examples/audience-two-datasets.json
    jq '.name = "Q3 rerun"' base.json | intuizi audiences create --file -

--frequency runs the dataset type's frequency analysis as the audience builds:
Visitation Frequency on POI, Apps Frequency on Apps and Web Frequency on
WebDomain, each counting the distinct days every device was seen. 'intuizi
activations preview' counts a range of those days, and an activation's
--freq-min and --freq-max export only that range. The analysis cannot be added
after the build, and it requires additional permissions which need to be
approved by your Account Manager; a 403 means they are not enabled for the
account.

--filter applies the POI quality filters. Without it a POI dataset gets the
default: anomalous_devices and strict_gps. --filter sends exactly the filters
listed, and --no-filters turns them all off. anomalous_devices
drops devices on Intuizi's list of known bad devices, anomalous_pois drops
every visit to locations flagged as anomalous, and strict_gps (GPS Strict
Filtering) drops every visit at a spot where more than 500 devices from one
signal provider were seen at that location in the same month: city-center
defaults and rounded coordinates reported for many devices at once. GPS
Strict Filtering is on by default. Turn it off (--no-filters, or --filter
anomalous_devices) for airports and stadiums, where real crowds can stand on
one spot. Audience Manager has the same default,
anomalous_devices and strict_gps. --filter and --no-filters apply to POI only,
and cannot be combined. --filter repeats for more than one:

    intuizi audiences create --type poi --brand "Example Offices" \
      --country USA --start-date 2026-06-01 --end-date 2026-08-31 \
      --name "Example Offices - summer" --dry-run

To see how many devices the audience would hold before building it, run the
same command line as 'intuizi audiences estimate create'.

Creation is asynchronous: the new audience comes back Initiating with a
results_count of 0. That is expected. Add --wait to block until the build
reaches Completed, with --timeout to bound it (default 60m). The last record
read is printed whether the build completes, fails, the wait times out or it
gives up after three failed reads in a row, and an audience that fails to
build, or a wait that times out or gives up, exits non-zero. A wait that times
out or gives up leaves the build running, and the error names the
'intuizi audiences show <id> --wait' that resumes it.

An audience that opts into data stream visualizations reads 109 Visualizing
data streams while it draws them, before Completed, and the wait goes on
through it. 107 Additional Info is a failure: the worker could not build the
audience as asked and stopped, so nothing follows it and the wait exits
non-zero. The API does not return the reason, but Audience Manager in the
Intuizi console shows it on the audience - most often a date range outside the
dataset's data coverage. Fix the request and create the audience again.

The request carries an Idempotency-Key, and a retry after a 429 reuses it.
Running the command again sends a fresh key and can create a second audience.
When a create gets no response at all, stderr prints the key it used: rerun
with --idempotency-key <key> to retry it without risking a duplicate.`,
		Args: cobra.NoArgs,
	}
	waitOpts := waitFlags(cmd)
	req.register(cmd)
	req.registerSwitching(cmd)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		return req.send(cmd, audienceLifecycle, wait, timeout,
			"still building - run 'intuizi audiences show <id> --wait' to follow it")
	}
	return cmd
}

// audienceRequest is the body that Create Audience and Estimate Audience Size
// both take, with the flags that build it: one dataset from flags, names
// resolved against the catalogs, or the whole body from --file. Both commands
// register the same flags and run the same checks, so an estimate is always
// of the audience the same command line would create.
type audienceRequest struct {
	file       string
	dryRun     bool
	dsType     string
	name       string
	startDate  string
	endDate    string
	brands     []string
	brandAll   []string
	categories []string
	providers  []string
	countries  []string
	states     []string
	cities     []string
	zipcodes   []string
	filters    []string
	noFilters  bool
	frequency  bool

	// Connectivity: valid with --type Connectivity only.
	networkKind  string
	networks     []string
	technologies []string
	minDays      int

	// SKUs: valid with --type SKUs only. --brand and --brand-all, above,
	// serve SKUs too.
	merchants  []string
	strictness int

	// Network Switching: registered on 'audiences create' only.
	switchFrom    string
	switchTo      string
	switchNetwork string
	switchMinDays int
}

// register adds the body flags to cmd.
func (r *audienceRequest) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&r.file, "file", "",
		`Path to the audience payload, or "-" to read it from stdin`)
	f.BoolVar(&r.dryRun, "dry-run", false,
		"Print the body the flags produce; creates nothing (names still resolve)")
	f.StringVar(&r.dsType, "type", "",
		"Dataset type, case-insensitive: poi, apps, webdomain, ctv,\n"+
			"affinitytransactions, deidentified, origin, connectivity or skus\n"+
			"(cohorts, demographics and profileattributes need --file)")
	f.StringVar(&r.name, "name", "", "Name for the audience")
	completeValues(cmd, "type", flagTypes())
	f.StringVar(&r.startDate, "start-date", "", "First day of the window, YYYY-MM-DD")
	f.StringVar(&r.endDate, "end-date", "", "Last day of the window, YYYY-MM-DD")
	// StringArray, not StringSlice: a city name may contain a comma.
	f.StringArrayVar(&r.brands, "brand", nil,
		"POI: a brand name or id. SKUs: a product brand key or name\n"+
			"(repeat the flag for more than one)")
	f.StringArrayVar(&r.brandAll, "brand-all", nil,
		"Take every brand matching this search, POI or SKUs (repeatable)")
	f.StringArrayVar(&r.categories, "category", nil,
		"Category name or id, per the dataset type\n(repeat the flag for more than one)")
	f.StringArrayVar(&r.providers, "provider", nil,
		"Signal provider id (repeatable; default every provider for the type;\n"+
			"not SKUs, which takes none)")
	f.StringArrayVar(&r.countries, "country", nil,
		"Country code, ISO-3 (repeat the flag for more than one)")
	f.StringArrayVar(&r.states, "state", nil,
		"State code (repeat the flag for more than one)")
	f.StringArrayVar(&r.cities, "city", nil,
		"City name (repeat the flag for more than one)")
	f.StringArrayVar(&r.zipcodes, "zipcode", nil,
		"Zip code (repeat the flag for more than one)")
	f.StringArrayVar(&r.filters, "filter", nil,
		"POI quality filter: anomalous_devices, anomalous_pois or strict_gps\n"+
			"(repeat the flag for more than one; sends exactly the ones listed.\n"+
			"Without --filter, POI uses the default: anomalous_devices and strict_gps)")
	completeValues(cmd, "filter", poiFilters)
	f.BoolVar(&r.noFilters, "no-filters", false,
		"Turn every POI quality filter off (sends an empty filter list);\n"+
			"cannot be combined with --filter")
	f.BoolVar(&r.frequency, "frequency", false,
		"Run the type's frequency analysis (POI, Apps or WebDomain), which\n"+
			"'activations preview' and --freq-min need; it cannot be added later")
	f.StringVar(&r.networkKind, "network-kind", "",
		"Connectivity only, and required there: home (home internet networks)\n"+
			"or mobile (mobile carriers)")
	completeValues(cmd, "network-kind", networkKinds)
	f.StringArrayVar(&r.networks, "network", nil,
		"Connectivity only: a network name from 'reference connectivity networks'\n"+
			"for the kind (repeatable; default any network of the kind)")
	f.StringArrayVar(&r.technologies, "technology", nil,
		"Connectivity home networks only: Fiber, Cable, DSL, Cable or DSL,\n"+
			"Satellite or Fixed wireless (repeatable; default any technology)")
	completeValues(cmd, "technology", connectivityTechnologies)
	f.IntVar(&r.minDays, "min-days", 0,
		"Connectivity only: the fewest days of a week a device must use the\n"+
			"network, 1 to 7 (the API's default is 3)")
	f.StringArrayVar(&r.merchants, "merchant", nil,
		"SKUs only: a merchant key or name from 'reference skus merchants'\n"+
			"(repeat the flag for more than one)")
	f.IntVar(&r.strictness, "match-strictness", 0,
		"SKUs only: Match Strictness, 1 (very strict) to 5 (more reach), how\n"+
			"strictly a device must be tied to the household of the purchase\n"+
			"(the API's default is 3)")
	completeValues(cmd, "match-strictness", matchStrictnessLevels)
	// No MarkFlagsOneRequired("file", "type"): cobra checks flag groups after
	// PersistentPreRunE, so its error exits 1. missingFlags covers --type.
}

// registerSwitching adds the Network Switching flags, which only 'audiences
// create' takes: an estimate and a quote never run the analysis.
func (r *audienceRequest) registerSwitching(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&r.switchFrom, "switch-from", "",
		"Network Switching: the first month to compare, YYYY-MM (needs --switch-to)")
	f.StringVar(&r.switchTo, "switch-to", "",
		"Network Switching: the second month to compare, YYYY-MM (needs\n"+
			"--switch-from); each month is read as its last full Monday-to-Sunday week")
	f.StringVar(&r.switchNetwork, "switch-network", "home",
		"Network Switching: compare each device's home internet network (home)\n"+
			"or its mobile carrier (mobile)")
	completeValues(cmd, "switch-network", networkKinds)
	f.IntVar(&r.switchMinDays, "switch-min-days", defaultMinHomeDays,
		"Network Switching: the fewest days of each month's week a device must\n"+
			"be seen on a network for it to be its home network, 2 to 5")
}

// send builds the body and posts it to lc.create: printed as created, or
// followed to its end with --wait. next is the hint a post without --wait
// ends on.
func (r *audienceRequest) send(cmd *cobra.Command, lc lifecycle, wait bool, timeout time.Duration, next string) error {
	if r.file == "" && r.dryRun && wait {
		return usageErr("--dry-run creates nothing, so there is nothing to --wait for")
	}
	body, printed, err := r.payload(cmd)
	if err != nil || printed {
		return err
	}
	return postBody(cmd, lc, body, wait, timeout, next)
}

// payload is the body the flags build, or the --file payload as it is. With
// --dry-run it prints the body instead, and printed says so: there is nothing
// left to send.
func (r *audienceRequest) payload(cmd *cobra.Command) (body any, printed bool, err error) {
	flags := cmd.Flags()

	if r.file != "" {
		if err := rejectBodyFlags(flags, audienceFields); err != nil {
			return nil, false, err
		}
		if r.dryRun {
			return nil, false, usageErr("--dry-run builds a body from flags; --file already has one")
		}
		raw, err := readPayload(cmd, r.file)
		if err != nil {
			return nil, false, err
		}
		return raw, false, nil
	}

	built, err := r.build(cmd)
	if err != nil {
		return nil, false, err
	}
	if r.dryRun {
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return nil, true, enc.Encode(built)
	}
	return built, false, nil
}

// build turns the flags into a single-dataset body. The checks that need no
// token come first, so a usage mistake costs no catalog read.
func (r *audienceRequest) build(cmd *cobra.Command) (audienceBody, error) {
	flags := cmd.Flags()
	var err error

	// The type first: a type the flags cannot build should not send the
	// caller off to add the dates it would then reject.
	if flags.Changed("type") {
		if r.dsType, err = canonicalType(r.dsType); err != nil {
			return audienceBody{}, err
		}
		if err := checkFlagType(r.dsType); err != nil {
			return audienceBody{}, err
		}
	}
	dsType := r.dsType
	// Switching is Connectivity only: say so before any missing flag.
	if flags.Changed("type") && dsType != "Connectivity" {
		for _, f := range switchingFlags {
			if flags.Changed(f) {
				return audienceBody{}, usageErr(errSwitchingType)
			}
		}
	}
	if err := missingFlags(flags, audienceRequired, "a single-dataset audience"); err != nil {
		return audienceBody{}, err
	}
	if err := nonEmpty("name", r.name); err != nil {
		return audienceBody{}, err
	}
	switching, err := r.switchingOptions(flags.Changed)
	if err != nil {
		return audienceBody{}, err
	}
	if err := parseWindow(r.startDate, r.endDate); err != nil {
		return audienceBody{}, err
	}
	if len(r.brands)+len(r.brandAll) > 0 && dsType != "POI" && dsType != "SKUs" {
		flag := "--brand"
		if len(r.brands) == 0 {
			flag = "--brand-all"
		}
		return audienceBody{}, usageErr(flag + " applies to --type POI and SKUs, not " + dsType + brandHint(dsType))
	}
	if len(r.filters) > 0 && dsType != "POI" {
		return audienceBody{}, usageErr("--filter applies to --type POI, not " + dsType)
	}
	if r.noFilters && dsType != "POI" {
		return audienceBody{}, usageErr("--no-filters applies to --type POI, not " + dsType)
	}
	if r.noFilters && flags.Changed("filter") {
		return audienceBody{}, usageErr("--no-filters and --filter cannot be used together")
	}
	// Before client(): a type with no category catalog is a usage error
	// whether or not there is a token, and so is one with no frequency
	// analysis.
	var cat categoryCatalog
	if len(r.categories) > 0 {
		if cat, err = categoryFor(dsType); err != nil {
			return audienceBody{}, err
		}
	}
	var analyses map[string]bool
	if r.frequency {
		key, ok := frequencyAnalyses[dsType]
		if !ok {
			return audienceBody{}, usageErr("--frequency applies to --type POI, Apps and WebDomain, not " + dsType)
		}
		analyses = map[string]bool{key: true}
	}
	for _, v := range []struct {
		flag   string
		values []string
	}{
		{"brand", r.brands}, {"brand-all", r.brandAll}, {"category", r.categories}, {"provider", r.providers},
		{"country", r.countries}, {"state", r.states}, {"city", r.cities}, {"zipcode", r.zipcodes},
		{"filter", r.filters},
	} {
		if err := nonEmpty(v.flag, v.values...); err != nil {
			return audienceBody{}, err
		}
	}
	canon, err := canonicalFilters(r.filters)
	if err != nil {
		return audienceBody{}, err
	}
	// nil: no flag, so the key is left out and the API applies its default
	// set. --no-filters: an empty list, which turns every filter off.
	var filters *[]string
	switch {
	case r.noFilters:
		filters = &[]string{}
	case len(canon) > 0:
		filters = &canon
	}
	conn, err := r.connectivityOptions(dsType, flags.Changed)
	if err != nil {
		return audienceBody{}, err
	}
	skus, err := r.skusOptions(dsType, flags.Changed)
	if err != nil {
		return audienceBody{}, err
	}
	// The API requires a country on Origin, and its geography is the
	// whole filter; a body without one is a 422.
	if dsType == "Origin" && len(r.countries) == 0 {
		return audienceBody{}, usageErr("--type Origin needs --country; list the covered countries with " +
			"'intuizi reference common countries --dataset-type Origin'")
	}

	c, err := client()
	if err != nil {
		return audienceBody{}, err
	}
	ctx := cmd.Context()

	ds := audienceDataset{Type: dsType, StartDate: r.startDate, EndDate: r.endDate, Filters: filters,
		ConnectivityNetworkKind: conn.kind, ConnectivityTechnologies: conn.technologies,
		ConnectivityMinDays: conn.minDays}

	// Read only when a name was given: without --network every network of
	// the kind is read, and nothing needs checking.
	if len(conn.networks) > 0 {
		catalog, err := connectivityNetworks(ctx, c, conn.kind)
		if err != nil {
			return audienceBody{}, err
		}
		if ds.ConnectivityNetworks, err = checkNetworks(conn.networks, catalog, conn.kind); err != nil {
			return audienceBody{}, err
		}
	}

	if dsType == "SKUs" {
		if ds.Merchants, err = resolveSkusKeys(ctx, c, skusMerchantsPath, "--merchant", skus.merchants,
			"merchants"); err != nil {
			return audienceBody{}, err
		}
		brands, err := resolveSkusKeys(ctx, c, skusBrandsPath, "--brand", skus.brands, "brands")
		if err != nil {
			return audienceBody{}, err
		}
		for _, search := range skus.brandAll {
			keys, err := skusAll(ctx, c, search)
			if err != nil {
				return audienceBody{}, err
			}
			noteBrandAll(cmd, search, len(keys))
			brands = append(brands, keys...)
		}
		// The API refuses a key named twice, and --brand-all can repeat
		// one --brand already named.
		ds.Brands = dedupe(brands)
		ds.MaxDevicesPerIP = skus.strictness
	} else {
		for _, b := range r.brands {
			id, err := resolveOrID(ctx, c, brandsPath, "--brand", b, "brands")
			if err != nil {
				return audienceBody{}, err
			}
			ds.Analysisdata = append(ds.Analysisdata, id)
		}
		for _, search := range r.brandAll {
			ids, err := resolveAll(ctx, c, brandsPath, search, "brands")
			if err != nil {
				return audienceBody{}, err
			}
			noteBrandAll(cmd, search, len(ids))
			ds.Analysisdata = append(ds.Analysisdata, ids...)
		}
	}
	if len(r.categories) > 0 {
		ids := make([]any, 0, len(r.categories))
		for _, name := range r.categories {
			id, err := resolveOrID(ctx, c, cat.path, "--category", name, "categories")
			if err != nil {
				return audienceBody{}, err
			}
			ids = append(ids, id)
		}
		// WebDomain reads the same selection from a different field.
		if cat.key == "iab_category_codes" {
			ds.IABCategoryCodes = ids
		} else {
			ds.Categories = ids
		}
	}

	// The catalog is read either way: it is the default, and the check
	// that a given --provider belongs to this type. SKUs takes no signal
	// providers, so there is nothing to read.
	if dsType != "SKUs" {
		all, err := allProviders(ctx, c, dsType)
		if err != nil {
			return audienceBody{}, err
		}
		if len(r.providers) == 0 {
			ds.SignalProviders = all
		} else if ds.SignalProviders, err = checkProviders(r.providers, all, dsType); err != nil {
			return audienceBody{}, err
		}
	}

	if len(r.countries)+len(r.states)+len(r.cities)+len(r.zipcodes) > 0 {
		ds.Location = &audienceLocation{
			Countries: r.countries, States: r.states, Cities: r.cities, Zipcodes: r.zipcodes,
		}
	}

	// Notes go to stderr, so a piped --dry-run stays the payload, and only
	// once every check has passed.
	// The dates stay as typed in the body; the API does the widening.
	if note := weeklyNote(dsType, r.startDate, r.endDate); note != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), note)
	}

	return audienceBody{Name: r.name, Datasets: []audienceDataset{ds}, Analyses: analyses,
		NetworkSwitching: switching}, nil
}

// noteBrandAll reports how many brands a --brand-all search selected. One
// word becoming nine ids should be visible; stderr keeps a piped --dry-run to
// the payload.
func noteBrandAll(cmd *cobra.Command, search string, n int) {
	noun := "brands"
	if n == 1 {
		noun = "brand"
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "--brand-all %q selected %d %s\n", search, n, noun)
}

// postBody posts body to lc.create and prints the record that comes back, or
// follows it to its end with --wait.
func postBody(cmd *cobra.Command, lc lifecycle, body any, wait bool, timeout time.Duration, next string) error {
	if wait {
		return createAndWait(cmd, lc, body, timeout)
	}
	return createBodyAs(cmd, lc.create, body, lc.lead, lc.view, next, lc.settled)
}

// brandHint points a --brand on another type at what that type does take.
// AffinityTransactions keeps brands in its own "brands" field, which no flag
// writes, and --category is only a way out for a type that has a catalog.
func brandHint(dsType string) string {
	switch dsType {
	case "AffinityTransactions":
		return "; affinity brands need --file"
	case "Origin":
		return "; Origin filters on geography only (--country, --state, --city, --zipcode)"
	case "Connectivity":
		return "; Connectivity filters by network (--network-kind, --network, --technology, --min-days) and geography"
	}
	if _, ok := categoryCatalogs[dsType]; ok {
		return "; use --category"
	}
	return ""
}

// audienceFields are the body-building flags, for --file to reject.
var audienceFields = []string{
	"type", "name", "start-date", "end-date",
	"brand", "brand-all", "category",
	"provider", "country", "state", "city", "zipcode", "filter", "no-filters", "frequency",
	"network-kind", "network", "technology", "min-days",
	"merchant", "match-strictness",
	"switch-from", "switch-to", "switch-network", "switch-min-days",
}

// audienceRequired is the minimum for a single dataset.
var audienceRequired = []string{"type", "name", "start-date", "end-date"}

// --------------------------------------------------------------------------------- show

// audiencesShowCommand is the generic showCommand plus --wait, so a build can be
// followed to Completed before it is activated. 108 Modeling and 109
// Visualizing data streams are waited through; 107 Additional Info ends it, and
// so do the 4xx errors, the 400 a cancelled lookalike ends at among them.
func audiencesShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one audience",
		Long: `Show one audience.

An audience built with Network Switching ('audiences create --switch-from')
is followed by a summary of its results: the two windows compared, how many
devices had a home network in each and in both, how many changed and
switched, and the top flows between two networks, the most devices first.
Every count is a count of devices. --json prints the results whole, with the
per-network rows, under .data[0].analysis_results.network_switching.

With --wait, keep polling until the build reaches Completed or fails, printing
each status change to stderr and the last record read to stdout, and exiting
non-zero if it fails, --timeout runs out or three reads in a row fail. A
lookalike in 108 Modeling is still training, and an audience in 109
Visualizing data streams is drawing its data stream visualizations, so both
are waited through:

    intuizi audiences show 1377 --wait

107 Additional Info is a failure: the build stopped and will not continue, so
the wait exits non-zero at once, and waiting again cannot change that. The
API does not return the reason, but Audience Manager in the Intuizi console
shows it on the audience. A cancelled lookalike reads 108 until the run
stops, then ends at 400 Error, so a wait on one exits non-zero there too. A
lookalike that stopped on a cancel before cancelled runs ended at 400 still
reads 108, so a wait on it ends only at --timeout.`,
		Args: cobra.ExactArgs(1),
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "audience")
		if err != nil {
			return err
		}
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}
		if !wait {
			return renderOneThen(cmd, audiencesPrefix+"/"+strconv.Itoa(id), audienceColumns, flatten,
				printNetworkSwitching)
		}
		c, err := client()
		if err != nil {
			return err
		}
		return waitAndPrint(cmd, c, audienceLifecycle, id, timeout)
	}
	return cmd
}

// --------------------------------------------------------------------------------- lookalikes

func audiencesLookalikeCommand() *cobra.Command {
	lookalike := &cobra.Command{
		Use:   "lookalike",
		Short: "Build and cancel Lookalike Model audiences",
		Long: `Build and cancel Lookalike Model audiences.

A Lookalike Model trains on a completed seed audience and produces a new
audience of similar devices. The Lookalike commands, create and cancel alike,
require additional permissions which need to be approved by your Account
Manager; a 403 means they are not enabled for the account.

Training shows as status 108 Modeling, which is not terminal - follow it with
'intuizi audiences show <id> --wait' until it reads Completed. A cancelled run
ends at 400 Error instead, which is final: see
'intuizi audiences lookalike cancel --help'.`,
	}

	create := lookalikeCreateCommand()

	cancel := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a lookalike run in progress",
		Long: `Cancel a lookalike run in progress.

The run stops at its next checkpoint, and until then the audience reads
108 Modeling. Once the run stops it ends at 400 Error, which is final: it
never reaches Completed. 'intuizi audiences show <id> --wait' follows a
cancelled run to that status and exits non-zero, as it does for any failed
build, and a webhook receiver gets audience.failed. The API returns only
400 Error, and Audience Manager in the Intuizi console shows the status as
"Cancelled on request." A run that has already finished cannot be
cancelled, and a cancel that arrives once the result is already being
published is ignored: the run completes. Remove a cancelled run with
'intuizi audiences delete <id>'.

Takes the id 'lookalike create' returned, not the seed's. Like create, it
requires additional permissions which need to be approved by your Account
Manager; a 403 means they are not enabled for the account.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "audience")
			if err != nil {
				return err
			}
			return postID(cmd, audiencesPrefix+"/cancel-lookalike", id,
				fmt.Sprintf("cancellation requested for audience %d - once the run stops "+
					"it ends at 400 Error, unless it was already publishing its result; "+
					"remove it with 'intuizi audiences delete %d'", id, id))
		},
	}

	lookalike.AddCommand(create, cancel, lookalikeQuoteCommand())
	return lookalike
}

// lookalikeGeo: countries is required even when states narrows it.
type lookalikeGeo struct {
	Countries []string `json:"countries"`
	States    []string `json:"states,omitempty"`
}

// lookalikeConfig mirrors the API's config block. The two booleans are
// required fields, so they are always sent, false included.
type lookalikeConfig struct {
	TargetSize         int          `json:"target_size"`
	Geo                lookalikeGeo `json:"geo"`
	Signals            []string     `json:"signals"`
	ExcludeSeedDevices bool         `json:"exclude_seed_devices"`
	ExpandEIDs         bool         `json:"expand_eids"`
	ContrastAudienceID int          `json:"contrast_audience_id,omitempty"`
}

// lookalikeBody always carries notification: the API defaults it to true, so
// omitting a false would send the email anyway.
type lookalikeBody struct {
	Name             string          `json:"name"`
	SourceAudienceID int             `json:"source_audience_id"`
	Notification     bool            `json:"notification"`
	Config           lookalikeConfig `json:"config"`
}

// lookalikeFields are the body-building flags, for --file to reject.
var lookalikeFields = []string{
	"name", "source-audience-id", "target-size", "signal", "country", "state",
	"exclude-seed-devices", "expand-eids", "contrast-audience-id", "notify",
}

// lookalikeRequired omits the two booleans: required by the API, but they
// default to false.
var lookalikeRequired = []string{"name", "source-audience-id", "target-size", "signal", "country"}

// maxLookalikeTarget is the API's cap on target_size.
const maxLookalikeTarget = 4_000_000

// lookalikeSignals: web and ctv were withdrawn and are rejected.
var lookalikeSignals = map[string]bool{
	"poi": true, "apps": true, "demographics": true,
	"transactions": true, "profile_attributes": true,
}

// lookalikeCreateLong is the help of 'audiences lookalike create'.
const lookalikeCreateLong = `Train a lookalike audience.

The body is one level deep, so it can be built from flags:

    intuizi audiences lookalike create \
      --name "Starbucks lookalike" --source-audience-id 1381 \
      --target-size 500000 --signal poi --signal apps --country USA

The seed must be Completed, must not itself be a lookalike, and must hold at
least 1,000 devices by default. --target-size is capped at 4,000,000.

--signal takes poi, apps, demographics, transactions or profile_attributes,
repeated for more than one. web and ctv are withdrawn and rejected.

--exclude-seed-devices and --expand-eids default to false and are always sent,
because the API requires both fields. --contrast-audience-id names a Completed,
non-lookalike audience other than the seed to contrast the seed against; the
seed's own id is refused before anything is sent. --dry-run prints the body
and sends nothing.

When the run completes, the API emails the user who created it. --notify is on
by default and always sent; pass --notify=false to skip the email.

Anything the API grows that these flags do not model goes through the whole
body instead, with --file:

    intuizi audiences lookalike create --file lookalike.json

Training shows as status 108 Modeling, which is not terminal - follow it with
'intuizi audiences show <id> --wait'. The Lookalike commands require
additional permissions which need to be approved by your Account Manager; a
403 means they are not enabled for the account.`

func lookalikeCreateCommand() *cobra.Command {
	path := audiencesPrefix + "/create-lookalike"
	next := "training - status 108 Modeling is not terminal; run " +
		"'intuizi audiences show <id> --wait' to follow it"

	return lookalikeBodyCommand("create", "Train a lookalike audience, from flags or a payload file", lookalikeCreateLong,
		func(cmd *cobra.Command, body any) error {
			return createBody(cmd, path, body, audienceColumns, next)
		})
}

// lookalikeBodyCommand is a command that takes the Create Lookalike Audience
// body, built from flags or read from --file, and hands it to send: the create
// itself, or its quote. Both run the same checks on the same flags, so a quote
// is always of the lookalike the same command line would train.
func lookalikeBodyCommand(use, short, long string, send func(cmd *cobra.Command, body any) error) *cobra.Command {
	var (
		file        string
		dryRun      bool
		name        string
		sourceID    int
		targetSize  int
		signals     []string
		countries   []string
		states      []string
		excludeSeed bool
		expandEIDs  bool
		contrastID  int
		notify      bool
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()

		if file != "" {
			if err := rejectBodyFlags(flags, lookalikeFields); err != nil {
				return err
			}
			if dryRun {
				return usageErr("--dry-run builds a body from flags; --file already has one")
			}
			payload, err := readPayload(cmd, file)
			if err != nil {
				return err
			}
			return send(cmd, payload)
		}

		if err := missingFlags(flags, lookalikeRequired, "a lookalike"); err != nil {
			return err
		}
		// Changed is true for "" and 0, so missingFlags alone let those through.
		if err := nonEmpty("name", name); err != nil {
			return err
		}
		if err := positiveID(flags, "source-audience-id", sourceID); err != nil {
			return err
		}
		if targetSize < 1 || targetSize > maxLookalikeTarget {
			return usageErr(fmt.Sprintf("--target-size must be between 1 and 4,000,000, not %d", targetSize))
		}
		for _, r := range []struct {
			flag   string
			values []string
		}{{"signal", signals}, {"country", countries}, {"state", states}} {
			if err := nonEmpty(r.flag, r.values...); err != nil {
				return err
			}
		}
		// A repeated --signal is one signal to the API too; send it once.
		signals = dedupe(signals)
		for _, sig := range signals {
			if !lookalikeSignals[sig] {
				return usageErr("--signal " + sig + " is not accepted; one of " +
					"apps, demographics, poi, profile_attributes, transactions")
			}
		}
		if err := positiveID(flags, "contrast-audience-id", contrastID); err != nil {
			return err
		}
		// The API's rule: a model cannot contrast the seed with itself.
		if flags.Changed("contrast-audience-id") && contrastID == sourceID {
			return usageErr(fmt.Sprintf("--contrast-audience-id %d is the seed; "+
				"contrast it against a different Completed, non-lookalike audience", contrastID))
		}

		body := lookalikeBody{
			Name:             name,
			SourceAudienceID: sourceID,
			Notification:     notify,
			Config: lookalikeConfig{
				TargetSize:         targetSize,
				Geo:                lookalikeGeo{Countries: countries, States: states},
				Signals:            signals,
				ExcludeSeedDevices: excludeSeed,
				ExpandEIDs:         expandEIDs,
				ContrastAudienceID: contrastID,
			},
		}

		if dryRun {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(body)
		}
		return send(cmd, body)
	}

	f := cmd.Flags()
	f.StringVar(&file, "file", "",
		`Path to the lookalike payload, or "-" to read it from stdin`)
	f.BoolVar(&dryRun, "dry-run", false,
		"Print the body the flags produce and send nothing")
	f.StringVar(&name, "name", "", "Name for the resulting audience")
	f.IntVar(&sourceID, "source-audience-id", 0,
		"Completed, non-lookalike seed audience with 1,000+ devices")
	f.IntVar(&targetSize, "target-size", 0,
		"Device count to aim for (capped at 4,000,000)")
	f.StringArrayVar(&signals, "signal", nil,
		"Data family to learn from (repeat the flag for more than one)")
	completeValues(cmd, "signal", sortedKeys(lookalikeSignals))
	f.StringArrayVar(&countries, "country", nil,
		"Country code, ISO-3 (repeat the flag for more than one)")
	f.StringArrayVar(&states, "state", nil,
		"State code (repeat the flag for more than one)")
	f.BoolVar(&excludeSeed, "exclude-seed-devices", false,
		"Leave the seed's own devices out of the result")
	f.BoolVar(&expandEIDs, "expand-eids", false,
		"Expand matched devices to their EIDs")
	f.IntVar(&contrastID, "contrast-audience-id", 0,
		"Completed, non-lookalike audience other than the seed, to contrast\nagainst")
	f.BoolVar(&notify, "notify", true,
		"Email the user who created the run when it completes\n(--notify=false to skip)")
	// No MarkFlagsOneRequired("file", "source-audience-id"): see audiencesCreateCommand.
	return cmd
}

// audiencesDeleteCommand is the shared delete, with the two things deleting an
// audience also does, per the API's delete service.
func audiencesDeleteCommand() *cobra.Command {
	cmd := deleteCommand("audience", audiencesPrefix)
	cmd.Long += `

A cohort created from a regular audience with 'intuizi cohorts create
--audience-id' is deleted with it; cohorts created from a Lookalike Model are
not. Deleting a Lookalike Model that is still modelling stops the run.`
	return cmd
}

func audiencesListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List audiences",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the audience name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, audiencesPrefix+"/index", query(), audienceColumns, "no audiences")
	}
	return cmd
}

func init() {
	audiencesCmd.AddCommand(
		audiencesListCommand(),
		audiencesShowCommand(),
		audiencesDeleteCommand(),
		audiencesCreateCommand(), audiencesLookalikeCommand(), audiencesEstimateCommand(),
		audiencesQuoteCommand(),
	)
	rootCmd.AddCommand(audiencesCmd)
}
