package cmd

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/spf13/pflag"

	"github.com/intuizi/intuizi-cli/internal/output"
)

var skusDimensions = []string{"brand", "merchant", "category", "total"}

// What an empty coverage answer says: an account without the SKUs dataset, or
// before the first daily update.
const skusEmptyNote = "an account without the SKUs dataset reads nothing, and so does any account " +
	"before the first daily update"

// An empty insights answer has one more cause: a row needs 10 home networks
// and 10 orders to be listed, so a narrow slice can read empty on any account.
const skusInsightsEmptyNote = skusEmptyNote + ". It also reads empty when no row passes the " +
	"filters (rows with fewer than 10 home networks or fewer than 10 orders are never listed)"

const skusCoverageLong = `Days of online purchases a SKUs dataset covers

Prints one record, one field per line, updated once a day:

  first_day            the first day with purchases
  newest_purchase_day  the newest day with purchases
  settled_day          two days before newest_purchase_day. A purchase day
                       keeps filling for about three days, so the days after
                       settled_day still read low
  newest_device_day    the newest purchase-click day with matched devices
                       (- before the first device refresh); a purchase
                       clicked after it adds no device yet
  empty_ranges         how many ranges of days have no purchases
  thin_days            how many days hold only a small part of their usual
                       purchases
  computed_at          when the figures were computed

A period with empty or thin days, or one that ends after settled_day or
newest_device_day, holds fewer purchases or fewer devices. The two lists are
in --json, as empty_days (a list of [first, last] pairs, both days included)
and thin_days.

The read takes no search or paging, and no flags of its own; the global
flags work (--quiet does not: the row has no id). An empty list means an
account without the SKUs dataset, or no daily update yet.`

const skusInsightsLong = `Spend, orders and networks by brand, merchant or category

Ranks what a SKUs dataset holds over the last --period days (7, 30 or 90),
ending on the settled day of "reference skus coverage". Updated once a day.

  --dimension brand|merchant|category   one row per brand, merchant or
                                        category
  --dimension total                     one row for the whole slice, with
                                        value ALL
  --dimension category                  lists the categories one level under
                                        --category (level 1 without it)

--quiet prints the values, one per line; it is refused with --dimension
total, whose one row is the total.

Columns: value (the key, or the category id), text, orders, spend (USD),
avg_order_value (USD), networks (home networks), devices (devices seen on
those home networks) and share_of_orders (a fraction of the slice's total
row: 0.1707 = 17.07%). A category row also ends with its path. --json adds
as_of and period_days.

Read the figures with care:
  - They are panel counts, not a merchant's sales.
  - networks and devices are approximate: never add them across rows.
  - Rows with fewer than 10 home networks, or fewer than 10 orders, are not
    listed, and devices is - below 10.
  - An order with two brands counts under both, so shares of brands can add
    up to more than 100%.

At most 50 rows are kept per slice, so --limit tops out at 50.

Pass keys and ids, not names: get them from "reference skus categories" and
"reference skus merchants". --merchant is not taken with --dimension
merchant. An empty list means an account without the SKUs dataset, no
daily update yet, or that no row passes the filters (see the 10 networks and
10 orders rule above).`

// skusInsightsCheck refuses what cannot be answered: --merchant with
// --dimension merchant (the API refuses it), and --quiet with --dimension total
// (its one row is the total, ALL, not a key to capture).
func skusInsightsCheck(flags *pflag.FlagSet) error {
	if !flags.Changed("dimension") {
		return nil
	}
	d, _ := flags.GetString("dimension")
	d = strings.ToLower(strings.TrimSpace(d))
	if d == "merchant" && flags.Changed("merchant") {
		return usageErr("--merchant cannot be combined with --dimension merchant")
	}
	if d == "total" && quietOutput {
		return usageErr("--quiet does not apply with --dimension total: its one row is the total, " +
			"not a key. Use --json")
	}
	return nil
}

var skusInsightsColumns = []string{"value", "text", "orders", "spend", "avg_order_value",
	"networks", "devices", "share_of_orders"}

// skusInsightsTable prints the columns in a fixed order, a category's path
// after its text, and a null as "-". --json has everything else (as_of,
// period_days).
func skusInsightsTable(items []output.Record) ([]output.Record, []string) {
	cols := append([]string(nil), skusInsightsColumns...)
	for _, it := range items {
		if _, ok := it["path"]; ok {
			cols = append(cols, "path") // last: it is long
			break
		}
	}
	rows := dashNulls(items, cols)
	for _, row := range rows {
		for _, k := range []string{"spend", "avg_order_value"} {
			if n, ok := row[k].(json.Number); ok {
				if f, err := n.Float64(); err == nil {
					row[k] = strconv.FormatFloat(f, 'f', 2, 64)
				}
			}
		}
	}
	return rows, cols
}

var skusCoverageColumns = []string{"first_day", "newest_purchase_day", "settled_day",
	"newest_device_day", "empty_ranges", "thin_days", "computed_at"}

// skusCoverageDetail turns the two lists into counts and returns the record in
// a fixed order, a null as "-"; the lists are in --json.
func skusCoverageDetail(items []output.Record) (output.Record, []string) {
	it := items[0]
	row := output.Record{}
	for _, k := range []string{"first_day", "newest_purchase_day", "settled_day", "newest_device_day", "computed_at"} {
		row[k] = it[k]
	}
	for from, to := range map[string]string{"empty_days": "empty_ranges", "thin_days": "thin_days"} {
		if list, ok := it[from].([]any); ok {
			row[to] = len(list)
		}
	}
	return dashNulls([]output.Record{row}, skusCoverageColumns)[0], skusCoverageColumns
}

// dashNulls prints a null or missing value as "-", so a row never reads as
// shifted and a gap is told from an empty string.
func dashNulls(items []output.Record, cols []string) []output.Record {
	rows := make([]output.Record, len(items))
	for i, it := range items {
		row := make(output.Record, len(cols))
		for _, c := range cols {
			if v, ok := it[c]; ok && v != nil {
				row[c] = v
			} else {
				row[c] = "-"
			}
		}
		rows[i] = row
	}
	return rows
}
