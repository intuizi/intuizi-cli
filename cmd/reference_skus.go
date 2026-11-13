package cmd

import (
	"strings"

	"github.com/spf13/pflag"

	"github.com/intuizi/intuizi-cli/internal/output"
)

var skusDimensions = []string{"brand", "merchant", "category", "total"}

const skusCoverageLong = `Days of online purchases a SKUs dataset covers

Prints one row: the first day with purchases, the newest purchase day, the
settled day (the newest day whose numbers no longer change), the newest day
with devices (- while there is none), how many ranges of days have no
purchases, how many days are thin, and when the figures were computed. The
lists of empty ranges and thin days are in --json (empty_days, thin_days).

Takes no flags. An account without the SKUs dataset reads nothing.`

const skusInsightsLong = `Spend, orders and networks by brand, merchant or category

Reads what a SKUs dataset holds, grouped by --dimension, over a
--period of 7, 30 or 90 days. Each row has the value (the key or id) and its text, orders,
spend, avg_order_value, networks (home networks, never summed across rows),
devices and share_of_orders (- where there is none), and the day the
figures are as of. A category row also has its path. --dimension total reads
one row for everything.

Pass keys and ids, not names: get them from "reference skus categories" and
"reference skus merchants". --merchant is not taken with --dimension
merchant.

An account without the SKUs dataset reads nothing.`

// skusInsightsCheck refuses the one combination the API refuses.
func skusInsightsCheck(flags *pflag.FlagSet) error {
	if !flags.Changed("merchant") || !flags.Changed("dimension") {
		return nil
	}
	d, _ := flags.GetString("dimension")
	if strings.ToLower(strings.TrimSpace(d)) == "merchant" {
		return usageErr("--merchant cannot be combined with --dimension merchant")
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
			cols = append(cols[:2], append([]string{"path"}, cols[2:]...)...)
			break
		}
	}
	return dashNulls(items, cols), cols
}

var skusCoverageColumns = []string{"first_day", "newest_purchase_day", "settled_day",
	"newest_device_day", "empty_ranges", "thin_days", "computed_at"}

// skusCoverageTable turns the two lists into counts; the lists are in --json.
func skusCoverageTable(items []output.Record) ([]output.Record, []string) {
	rows := make([]output.Record, len(items))
	for i, it := range items {
		row := output.Record{}
		for _, k := range []string{"first_day", "newest_purchase_day", "settled_day", "newest_device_day", "computed_at"} {
			row[k] = it[k]
		}
		for from, to := range map[string]string{"empty_days": "empty_ranges", "thin_days": "thin_days"} {
			if list, ok := it[from].([]any); ok {
				row[to] = len(list)
			}
		}
		rows[i] = row
	}
	return dashNulls(rows, skusCoverageColumns), skusCoverageColumns
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
