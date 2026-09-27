package cmd

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// previewLead is what a preview leads with: the count for the range asked,
// the range, and what bounds it.
var previewLead = []string{
	"audience", "filtered_count", "freq_range", "frequency_bounds",
	"source_count", "histogram_total", "filter_hash", "as_of",
}

func activationsPreviewCommand() *cobra.Command {
	var audienceID, freqMin, freqMax int

	cmd := &cobra.Command{
		Use:   "preview",
		Short: "Count the devices a frequency filter would export",
		Long: `Count the devices an activation's frequency filter would export.

A read-only dry run of the filter an activation applies to a Completed
audience: the number of devices seen on at least --freq-min and at most
--freq-max distinct days in the audience's date window, both inclusive. It is
the count Audience Manager shows as Limit Audience for the same Freq. Range.
Nothing is created, exported or billed, so preview as many ranges as you like:

    intuizi activations preview --audience-id 88 --freq-min 2 --freq-max 5

The output leads with filtered_count for the range, the frequency_bounds a
range must fall inside, and the audience's totals, then prints the whole
histogram: devices per number of distinct days. Use the upper bound as
--freq-max for an open-ended "2+" range. --json adds the limitations, which
explain how each count is made.

The audience must have been built with a frequency analysis - 'intuizi
audiences create --frequency', or the matching analyses key in a --file body -
and it cannot be added after the build. A day-part frequency analysis, a
Lookalike Model or a cohort cannot be previewed.

To export exactly what was previewed, pass the same range and the
filter_hash it printed to the create:

    intuizi activations create --audience-id 88 \
      --endpoint-connection-id 12 --pricing-model-id 3 --datastream 7 \
      --freq-min 2 --freq-max 5 --filter-hash sha256:4f9d...

The API recomputes the hash from the audience as stored then, so a different
range or an audience rebuilt since the preview is refused rather than
exported.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			// A preview is a count, not a resource, so there is no id to print.
			if quietOutput {
				return usageErr("activations preview returns a device count, not an id - --quiet does not apply")
			}
			if missing := unsetFlags(flags, "audience-id", "freq-min", "freq-max"); len(missing) > 0 {
				return usageErr("a preview needs " + strings.Join(missing, ", "))
			}
			if err := positiveID(flags, "audience-id", audienceID); err != nil {
				return err
			}
			if err := checkFreqRange(freqMin, freqMax); err != nil {
				return err
			}

			query := url.Values{}
			query.Set("audience_id", strconv.Itoa(audienceID))
			query.Set("freq_min", strconv.Itoa(freqMin))
			query.Set("freq_max", strconv.Itoa(freqMax))
			return previewActivation(cmd, query)
		},
	}

	flags := cmd.Flags()
	flags.IntVar(&audienceID, "audience-id", 0,
		"A Completed audience built with a frequency analysis")
	flags.IntVar(&freqMin, "freq-min", 0,
		"Fewest distinct days a device was seen, inclusive")
	flags.IntVar(&freqMax, "freq-max", 0,
		"Most distinct days a device was seen, inclusive; the upper\nfrequency bound for an open-ended range")
	return cmd
}

// checkFreqRange refuses a range the API would 422: a negative bound, or one
// that is upside down. Whether it fits the audience's frequency_bounds only
// the API knows, so that is left to it.
func checkFreqRange(lo, hi int) error {
	if lo < 0 || hi < 0 {
		return usageErr(fmt.Sprintf("--freq-min %d and --freq-max %d count days, so neither can be negative", lo, hi))
	}
	if lo > hi {
		return usageErr(fmt.Sprintf("--freq-min %d is above --freq-max %d", lo, hi))
	}
	return nil
}

// previewActivation performs the read. Not renderOne: the histogram is the
// point of a preview, and the detail view would count it as "[4 items]".
func previewActivation(cmd *cobra.Command, query url.Values) error {
	c, err := client()
	if err != nil {
		return err
	}
	path := activationsPrefix + "/preview"

	if jsonOutput {
		raw, err := c.GetRaw(cmd.Context(), path, query)
		if err != nil {
			printErrorEnvelope(cmd, err)
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	rec, err := api.Read[output.Record](cmd.Context(), c, path, query)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if err := output.Detail(out, previewView(rec), previewLead); err != nil {
		return err
	}
	rows := histogramRows(rec)
	if len(rows) == 0 {
		return nil
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	return output.TableWith(out, rows, []string{"days", "devices"})
}

// previewView reads the nested fields a person picks a range from as plain
// values: the range applied and the bounds as "2-5", the recency window as
// "POI 2026-06-01..2026-07-31", and the audience's activation verdict. The
// histogram prints as its own table and the limitations are left to --json.
func previewView(rec output.Record) output.Record {
	view := flatten(rec)
	delete(view, "histogram")
	delete(view, "limitations")
	delete(view, "applied_filters")

	if filters, ok := rec["applied_filters"].(map[string]any); ok {
		if r, ok := span(filters["freq_min"], filters["freq_max"]); ok {
			view["freq_range"] = r
		}
	}
	if bounds, ok := rec["frequency_bounds"].(map[string]any); ok {
		if r, ok := span(bounds["min"], bounds["max"]); ok {
			view["frequency_bounds"] = r
		}
	}
	if windows, ok := rec["recency"].([]any); ok {
		parts := make([]string, 0, len(windows))
		for _, w := range windows {
			m, _ := w.(map[string]any)
			parts = append(parts, fmt.Sprintf("%v %v..%v", m["dataset_type"], m["start_date"], m["end_date"]))
		}
		view["recency"] = strings.Join(parts, ", ")
	}
	if audience, ok := rec["audience"].(map[string]any); ok {
		if allowed, ok := audience["is_activation_allowed"]; ok {
			view["is_activation_allowed"] = allowed
		}
	}
	return view
}

// span renders two bounds as "lo-hi", when both are there.
func span(lo, hi any) (string, bool) {
	if lo == nil || hi == nil {
		return "", false
	}
	return fmt.Sprintf("%v-%v", lo, hi), true
}

// histogramRows turns the histogram's {index, counts} buckets into rows of
// distinct days and devices.
func histogramRows(rec output.Record) []output.Record {
	buckets, _ := rec["histogram"].([]any)
	rows := make([]output.Record, 0, len(buckets))
	for _, b := range buckets {
		m, _ := b.(map[string]any)
		rows = append(rows, output.Record{"days": m["index"], "devices": m["counts"]})
	}
	return rows
}
