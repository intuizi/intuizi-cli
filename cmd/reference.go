package cmd

import "github.com/spf13/cobra"

// Every documented reference read, as a table rather than one function per
// endpoint: endpoint.command in reference_run.go turns each row into a cobra
// command.
//
// Adding a row also needs an update to wantPaths in reference_test.go.
// Contracts: docs-src/content/api/v2 in intuizi-console.

// kind decides a param's flag type and how it is written into the query. Array
// params repeat under a "[]"-suffixed key; a comma-joined value would arrive as
// one string.
type kind int

const (
	str kind = iota
	num
	strs
	nums
)

// param is one endpoint-specific query parameter, named exactly as the API wants
// it; flagName derives the flag. search, page and per_page are never listed -
// they come from the noSearch and paged fields.
type param struct {
	name     string
	kind     kind
	required bool
	help     string
}

// endpoint is one reference read. path is set only where the URL segment differs
// from the command name. paged adds --page and --per-page; noSearch drops
// --search, for a read that takes no parameters at all. noID marks rows that
// carry neither id nor value, so --quiet is refused before the round trip
// rather than failing after it.
type endpoint struct {
	item     string
	path     string
	short    string
	paged    bool
	noSearch bool
	noID     bool
	params   []param
}

// referenceGroup is one URL segment below /analyses/reference: reference <group> <item>.
// path overrides name as that URL segment, so a group can be typed the way the
// console names it even where the API spells it differently.
type referenceGroup struct {
	name      string
	path      string
	short     string
	endpoints []endpoint
}

var referenceGroups = []referenceGroup{
	{
		name:  "common",
		short: "Catalogs shared across every dataset type",
		endpoints: []endpoint{
			{item: "dataset-types", short: "Dataset types an audience can be built from"},
			{item: "countries", short: "Countries", params: []param{
				{name: "datasetType", kind: str, help: "Limit to one dataset type (e.g. WebDomain)"},
			}},
			{item: "states", short: "States, for the given countries", params: []param{
				{name: "countries", kind: strs, required: true, help: "Country codes, ISO-3 (e.g. USA)"},
			}},
			{item: "cities", short: "Cities, for the given states", params: []param{
				{name: "states", kind: strs, required: true, help: "State codes (e.g. NY)"},
				{name: "dmas", kind: strs, help: "Narrow by DMA label"},
			}},
			{item: "dmas", short: "DMAs, for the given countries", params: []param{
				{name: "countries", kind: strs, required: true, help: "Country codes, ISO-3 (e.g. USA)"},
				{name: "states", kind: strs, help: "Narrow by state code"},
				{name: "cities", kind: strs, help: "Narrow by city name"},
			}},
			{item: "zipcodes", short: "Zip codes, for the given cities", paged: true, params: []param{
				{name: "cities", kind: strs, required: true, help: "City names (e.g. Los Angeles)"},
				{name: "states", kind: strs, help: "Narrow by state code"},
				{name: "dmas", kind: strs, help: "Narrow by DMA label"},
				{name: "countries", kind: strs, help: "Narrow by country code, ISO-3 (e.g. USA)"},
			}},
			{item: "operators", short: "Boolean operators for combining datasets"},
			{item: "languages", short: "Languages", paged: true},
			{item: "signal-providers", short: "Signal providers for a dataset type", params: []param{
				{name: "dataType", kind: str, required: true, help: "Dataset type (e.g. WebDomain)"},
			}},
			{item: "endpoint-partners", short: "Activation endpoint partners"},
			{item: "endpoint-connections", short: "Your company's endpoint connections"},
			{item: "pricing-models", short: "Pricing models for an endpoint partner", params: []param{
				{name: "partner_id", kind: num, required: true, help: "Endpoint partner id"},
			}},
			{item: "datastreams", short: "Datastreams for an endpoint partner", params: []param{
				{name: "partner_id", kind: num, required: true, help: "Endpoint partner id"},
			}},
			{item: "schedule-frequencies", short: "Schedule frequencies"},
			{item: "schedule-windows", short: "Schedule date windows"},
			{item: "schedule-endings", short: "Schedule ending rules"},
		},
	},
	{
		name:  "poi",
		short: "POI segments, categories, brands and locations",
		endpoints: []endpoint{
			{item: "segments", short: "POI segments"},
			{item: "categories", short: "POI categories", params: []param{
				{name: "segments", kind: nums, help: "Segment ids to cascade from"},
			}},
			{item: "brands", short: "POI brands", params: []param{
				{name: "categories", kind: nums, help: "Category ids to cascade from"},
			}},
			{item: "locations", short: "POI locations", paged: true, params: []param{
				{name: "brands", kind: nums, help: "Brand ids to cascade from"},
			}},
		},
	},
	{
		name:  "apps",
		short: "App categories, tags, OSes, bundle ids and taxonomies",
		endpoints: []endpoint{
			{item: "categories", short: "App categories", paged: true},
			{item: "tags", short: "App tags", paged: true},
			{item: "os", short: "App operating systems"},
			{item: "bundle-ids", short: "App bundle ids", paged: true, params: []param{
				{name: "categories", kind: nums, help: "Category ids to filter by"},
				{name: "taxonomies", kind: nums, help: "Taxonomy ids to filter by (takes precedence over categories)"},
			}},
			{item: "taxonomies", short: "App taxonomies", paged: true, params: []param{
				{name: "categories", kind: nums, help: "Category ids to filter by"},
			}},
		},
	},
	{
		name:  "ctv",
		short: "Connected TV vendors, content, channels and devices",
		endpoints: []endpoint{
			{item: "vendors", short: "CTV vendors"},
			{item: "content-types", short: "CTV content types"},
			{item: "content-genres", short: "CTV content genres"},
			{item: "channel-names", short: "CTV channel names"},
			{item: "device-types", short: "CTV device types", paged: true},
			{item: "device-makes", short: "CTV device makes", paged: true},
			{item: "device-oses", short: "CTV device operating systems", paged: true},
			{item: "connection-types", short: "CTV connection types", paged: true},
			{item: "isps", short: "CTV ISPs and carriers", paged: true},
			{item: "series", short: "CTV series", paged: true},
		},
	},
	{
		name:  "web",
		short: "IAB categories, web domains, browsers and devices",
		endpoints: []endpoint{
			{item: "iab-categories", short: "IAB categories"},
			{item: "iab-subcategories", short: "IAB subcategories", params: []param{
				{name: "category_ids", kind: strs, help: "Parent IAB category ids or codes"},
			}},
			{item: "domains", short: "Web domains", paged: true, params: []param{
				{name: "category_codes", kind: strs, help: "IAB category codes (e.g. IAB2)"},
				{name: "subcategory_codes", kind: strs, help: "IAB subcategory codes (e.g. IAB2-1); takes precedence over category codes"},
			}},
			{item: "ref-domains", short: "Referrer domains", paged: true},
			{item: "browsers", short: "Browsers", paged: true},
			{item: "device-types", short: "Web device types", paged: true},
			{item: "device-makes", short: "Web device makes", paged: true},
			{item: "device-oses", short: "Web device operating systems", paged: true},
		},
	},
	{
		name:  "transactions",
		path:  "affinity-transactions",
		short: "Affinity purchase categories, brands and demographics",
		endpoints: []endpoint{
			{item: "categories", short: "Affinity categories"},
			{item: "subcategories", short: "Affinity subcategories", paged: true, params: []param{
				{name: "categories", kind: nums, help: "Affinity category ids to cascade from"},
			}},
			{item: "brands", short: "Affinity brands", paged: true, params: []param{
				{name: "categories", kind: nums, help: "Affinity category ids to cascade from"},
				{name: "subcategories", kind: strs, help: "Subcategories to cascade from"},
			}},
			{item: "incomes", short: "Affinity income bands", paged: true},
			{item: "ages", short: "Affinity age bands", paged: true},
			{item: "genders", short: "Affinity genders", paged: true},
			{item: "ethnicities", short: "Affinity ethnicities", paged: true},
		},
	},
	{
		name:  "demographics",
		short: "Gender, age, marital status and income dictionaries",
		endpoints: []endpoint{
			{item: "genders", short: "Demographic genders", paged: true},
			{item: "ages", short: "Demographic age ranges", paged: true},
			{item: "marital-statuses", short: "Demographic marital statuses", paged: true},
			{item: "incomes", short: "Demographic income ranges", paged: true},
		},
	},
	{
		name:  "profile-attributes",
		short: "Profile attribute categories, keys, values and date bounds",
		endpoints: []endpoint{
			{item: "categories", short: "Profile attribute categories", paged: true},
			{item: "keys", short: "Profile attribute keys", paged: true, params: []param{
				{name: "category_ids", kind: nums, help: "Category ids to cascade from"},
			}},
			{item: "values", short: "Profile attribute values", paged: true, params: []param{
				{name: "category_ids", kind: nums, help: "Category ids to cascade from"},
				{name: "key", kind: str, help: "The key to cascade from"},
			}},
			// The only read with no query parameters at all, search included,
			// and the only one whose rows - {start_limit, end_limit} - have
			// nothing --quiet could print.
			{item: "recency-limits", short: "The delivered quarter window dates must fall inside", noSearch: true, noID: true},
		},
	},
	{
		name:  "deidentified",
		short: "Signal fields a deidentified delivery can carry",
		endpoints: []endpoint{
			{item: "fields", short: "Deidentified signal fields", params: []param{
				{name: "group", kind: str, help: "Narrow to one group, using a group value from this same read"},
			}},
		},
	},
	{
		name:  "cohorts",
		short: "Your company's completed cohorts",
		endpoints: []endpoint{
			// The URL segment really is get-cohorts.
			{item: "list", path: "get-cohorts", short: "Completed cohorts"},
		},
	},
}

var referenceCmd = &cobra.Command{
	Use:   "reference",
	Short: "Access reference data",
	Long: `Read the catalogs an audience payload is built from.

Every reference read is a GET with no side effects, so these are safe to explore.
Ids and values collected here are what 'intuizi audiences create' expects.

    intuizi reference common dataset-types
    intuizi reference common states --countries USA
    intuizi reference apps categories --search fitness
    intuizi reference web domains --category-codes IAB2 --json

Each read accepts --search, a case-insensitive contains match on the item label.
Paginated reads add --page and --per-page, and return one page per call.`,
}

func init() {
	rootCmd.AddCommand(referenceCmd)
}
