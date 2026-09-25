package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

// audiencesPrefix is the path every audience call hangs off.
const audiencesPrefix = "/analyses/audiences"

// audienceColumns lead both the list table and the show view. The index returns
// thirteen fields per audience; --json prints all of them.
var audienceColumns = []string{"id", "name", "status", "results_count", "created_at"}

var audiencesCmd = &cobra.Command{
	Use:   "audiences",
	Short: "Manage audiences",
	Long: `Manage audiences.

An audience defines a group of devices drawn from one or two datasets - places
visited, apps used, CTV viewing, web activity. Delivering one somewhere is a
separate step: see 'intuizi activations'.

Building is asynchronous. 'audiences create' returns as soon as the audience is
queued, in an Initiating state; add --wait to 'create' or 'show' to block until
it reads Completed. An audience that is still building is not a failure.

The ids and codes a payload is built from come from 'intuizi reference'.`,
}

// --------------------------------------------------------------------------------- create

func audiencesCreateCommand() *cobra.Command {
	var (
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
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an audience, from flags or a payload file",
		Long: `Create an audience.

A single-dataset audience has a shallow body, so it can be built from flags,
with names resolved against the reference catalogs rather than pasted as ids:

    intuizi audiences create \
      --type POI --brand starbucks \
      --country USA --state CA --city "San Francisco" \
      --start-date 2026-09-02 --end-date 2026-09-09 \
      --name "Starbucks visitors - SF - 1 week"

--brand and --category both take a name or an id; a name matching no entry, or
more than one, is an error listing what was found. --brand-all takes every
match for a search instead, for the deliberate "all the coffee brands" case,
and reports to stderr how many it selected. --brand applies to POI.
--category applies to POI, Apps, WebDomain and AffinityTransactions, each
resolving against its own catalog. Every selector repeats for more than one
value.

Omitting --provider includes every signal provider for the dataset type, which
is almost always what you want: a provider left out builds an audience that
completes with zero devices and no error. A --provider the type's catalog does
not list is rejected, since the API would accept it and build that same empty
audience.

--dry-run prints the body those flags produce and sends nothing, so it doubles
as a starting point for the file form:

    intuizi audiences create --type POI ... --dry-run > audience.json

Two datasets need an operator, and refine, crossvisitation and crosspurchase
are nested, so those are passed whole instead. The file is forwarded untouched,
so a field this CLI has never heard of still reaches the API:

    intuizi audiences create --file examples/audience-two-datasets.json
    jq '.name = "Q3 rerun"' base.json | intuizi audiences create --file -

Creation is asynchronous: the new audience comes back Initiating with a
results_count of 0. That is expected. Add --wait to block until the build
reaches Completed, with --timeout to bound it (default 60m); the final record
is printed and an audience that fails to build exits non-zero.

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate.`,
		Args: cobra.NoArgs,
	}
	waitOpts := waitFlags(cmd)

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()
		wait, timeout, err := waitOpts()
		if err != nil {
			return err
		}

		if file != "" {
			if err := rejectBodyFlags(flags, audienceFields); err != nil {
				return err
			}
			if dryRun {
				return usageErr("--dry-run builds a body from flags; --file already has one")
			}
			if !wait {
				return createFromFile(cmd, audiencesPrefix+"/create", file, audienceColumns,
					"still building - run 'intuizi audiences show <id> --wait' to follow it")
			}
			payload, err := readPayload(cmd, file)
			if err != nil {
				return err
			}
			return createAndWait(cmd, audiencesPrefix, "audience", payload, audienceColumns, timeout)
		}

		if dryRun && wait {
			return usageErr("--dry-run sends nothing, so there is nothing to --wait for")
		}
		if err := missingFlags(flags, audienceRequired, "a single-dataset audience"); err != nil {
			return err
		}
		if err := nonEmpty("name", name); err != nil {
			return err
		}
		if err := parseWindow(startDate, endDate); err != nil {
			return err
		}
		dsType, err = canonicalType(dsType)
		if err != nil {
			return err
		}
		if len(brands)+len(brandAll) > 0 && dsType != "POI" {
			// AffinityTransactions keeps brands in its own "brands" field, which
			// no flag writes - so do not point at --category, a different filter.
			flag := "--brand"
			if len(brands) == 0 {
				flag = "--brand-all"
			}
			hint := "; use --category"
			if dsType == "AffinityTransactions" {
				hint = "; affinity brands need --file"
			}
			return usageErr(flag + " applies to --type POI, not " + dsType + hint)
		}
		// Before client(): a type with no category catalog is a usage error
		// whether or not there is a token.
		var cat categoryCatalog
		if len(categories) > 0 {
			if cat, err = categoryFor(dsType); err != nil {
				return err
			}
		}
		for _, r := range []struct {
			flag   string
			values []string
		}{
			{"brand", brands}, {"brand-all", brandAll}, {"category", categories}, {"provider", providers},
			{"country", countries}, {"state", states}, {"city", cities}, {"zipcode", zipcodes},
		} {
			if err := nonEmpty(r.flag, r.values...); err != nil {
				return err
			}
		}

		c, err := client()
		if err != nil {
			return err
		}
		ctx := cmd.Context()

		ds := audienceDataset{Type: dsType, StartDate: startDate, EndDate: endDate}

		for _, b := range brands {
			id, err := resolveOrID(ctx, c, brandsPath, "--brand", b, "brands")
			if err != nil {
				return err
			}
			ds.Analysisdata = append(ds.Analysisdata, id)
		}
		for _, search := range brandAll {
			ids, err := resolveAll(ctx, c, brandsPath, search, "brands")
			if err != nil {
				return err
			}
			// One word becoming nine ids should be visible; stderr keeps a
			// piped --dry-run to the payload.
			noun := "brands"
			if len(ids) == 1 {
				noun = "brand"
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "--brand-all %q selected %d %s\n",
				search, len(ids), noun)
			ds.Analysisdata = append(ds.Analysisdata, ids...)
		}
		if len(categories) > 0 {
			ids := make([]any, 0, len(categories))
			for _, name := range categories {
				id, err := resolveOrID(ctx, c, cat.path, "--category", name, "categories")
				if err != nil {
					return err
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
		// that a given --provider belongs to this type.
		all, err := allProviders(ctx, c, dsType)
		if err != nil {
			return err
		}
		if len(providers) == 0 {
			ds.SignalProviders = all
		} else if ds.SignalProviders, err = checkProviders(providers, all, dsType); err != nil {
			return err
		}

		if len(countries)+len(states)+len(cities)+len(zipcodes) > 0 {
			ds.Location = &audienceLocation{
				Countries: countries, States: states, Cities: cities, Zipcodes: zipcodes,
			}
		}

		body := audienceBody{Name: name, Datasets: []audienceDataset{ds}}

		if dryRun {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(body)
		}
		if !wait {
			return createBody(cmd, audiencesPrefix+"/create", body, audienceColumns,
				"still building - run 'intuizi audiences show <id> --wait' to follow it")
		}
		return createAndWait(cmd, audiencesPrefix, "audience", body, audienceColumns, timeout)
	}

	f := cmd.Flags()
	f.StringVar(&file, "file", "",
		`Path to the audience payload, or "-" to read it from stdin`)
	f.BoolVar(&dryRun, "dry-run", false,
		"Print the body the flags produce; creates nothing (names still resolve)")
	f.StringVar(&dsType, "type", "",
		"Dataset type, case-insensitive: poi, apps, webdomain, ctv, cohorts,\n"+
			"affinitytransactions, demographics, deidentified or profileattributes")
	f.StringVar(&name, "name", "", "Name for the audience")
	completeValues(cmd, "type", sortedKeys(datasetTypes))
	f.StringVar(&startDate, "start-date", "", "First day of the window, YYYY-MM-DD")
	f.StringVar(&endDate, "end-date", "", "Last day of the window, YYYY-MM-DD")
	// StringArray, not StringSlice: a city name may contain a comma.
	f.StringArrayVar(&brands, "brand", nil,
		"Brand name or id, POI only (repeat the flag for more than one)")
	f.StringArrayVar(&brandAll, "brand-all", nil,
		"Take every brand matching this search, POI only (repeatable)")
	f.StringArrayVar(&categories, "category", nil,
		"Category name or id, per the dataset type\n(repeat the flag for more than one)")
	f.StringArrayVar(&providers, "provider", nil,
		"Signal provider id (repeatable; default every provider for the type)")
	f.StringArrayVar(&countries, "country", nil,
		"Country code, ISO-3 (repeat the flag for more than one)")
	f.StringArrayVar(&states, "state", nil,
		"State code (repeat the flag for more than one)")
	f.StringArrayVar(&cities, "city", nil,
		"City name (repeat the flag for more than one)")
	f.StringArrayVar(&zipcodes, "zipcode", nil,
		"Zip code (repeat the flag for more than one)")
	// No MarkFlagsOneRequired("file", "type"): cobra checks flag groups after
	// PersistentPreRunE, so its error exits 1. missingFlags covers --type.
	return cmd
}

// audienceFields are the body-building flags, for --file to reject.
var audienceFields = []string{
	"type", "name", "start-date", "end-date",
	"brand", "brand-all", "category",
	"provider", "country", "state", "city", "zipcode",
}

// audienceRequired is the minimum for a single dataset.
var audienceRequired = []string{"type", "name", "start-date", "end-date"}

// --------------------------------------------------------------------------------- show

// audiencesShowCommand is the generic showCommand plus --wait, so a build can be
// followed to Completed before it is activated. 108 Modeling is waited through.
func audiencesShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one audience",
		Long: `Show one audience.

With --wait, keep polling until the build reaches Completed or fails, printing
each status change to stderr. A lookalike in 108 Modeling is still training and
is waited through:

    intuizi audiences show 1377 --wait`,
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
			return renderOne(cmd, audiencesPrefix+"/"+strconv.Itoa(id), audienceColumns)
		}
		c, err := client()
		if err != nil {
			return err
		}
		return waitAndPrint(cmd, c, audiencesPrefix, "audience", id, audienceColumns, timeout)
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
audience of similar devices. It is a gated feature: without the permission the
create is rejected with 403.

Training shows as status 108 Modeling, which is not terminal - keep polling
'audiences show <id>' until it reads Completed.`,
	}

	create := lookalikeCreateCommand()

	cancel := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a lookalike run in progress",
		Long: `Cancel a lookalike run in progress.

The job stops at its next checkpoint rather than immediately, so the audience
may sit in its current status for a short while after this returns.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "audience")
			if err != nil {
				return err
			}
			return postID(cmd, audiencesPrefix+"/cancel-lookalike", id,
				fmt.Sprintf("cancellation requested for audience %d", id))
		},
	}

	lookalike.AddCommand(create, cancel)
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

type lookalikeBody struct {
	Name             string          `json:"name"`
	SourceAudienceID int             `json:"source_audience_id"`
	Notification     bool            `json:"notification,omitempty"`
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

func lookalikeCreateCommand() *cobra.Command {
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
		Use:   "create",
		Short: "Train a lookalike audience, from flags or a payload file",
		Long: `Train a lookalike audience.

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
non-lookalike audience to contrast the seed against. --dry-run prints the body
and sends nothing.

Anything the API grows that these flags do not model goes through the whole
body instead, with --file:

    intuizi audiences lookalike create --file lookalike.json

Training shows as status 108 Modeling, which is not terminal - keep polling.
Requires the Lookalike capability; a 403 means it is not enabled.`,
		Args: cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()
		path := audiencesPrefix + "/create-lookalike"
		next := "training - status 108 Modeling is not terminal, keep polling"

		if file != "" {
			if err := rejectBodyFlags(flags, lookalikeFields); err != nil {
				return err
			}
			if dryRun {
				return usageErr("--dry-run builds a body from flags; --file already has one")
			}
			return createFromFile(cmd, path, file, audienceColumns, next)
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
		return createBody(cmd, path, body, audienceColumns, next)
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
		"Completed, non-lookalike audience to contrast against")
	f.BoolVar(&notify, "notify", false,
		"Notify the account owner when the run finishes")
	// No MarkFlagsOneRequired("file", "source-audience-id"): see audiencesCreateCommand.
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
		deleteCommand("audience", audiencesPrefix),
		audiencesCreateCommand(), audiencesLookalikeCommand(),
	)
	rootCmd.AddCommand(audiencesCmd)
}
