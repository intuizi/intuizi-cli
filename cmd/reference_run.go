package cmd

import (
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// referencePrefix is the path every reference read hangs off.
const referencePrefix = "/analyses/reference/"

func init() {
	for _, g := range referenceGroups {
		groupCmd := &cobra.Command{
			Use:   g.name,
			Short: g.short,
		}
		segment := g.path
		if segment == "" {
			segment = g.name
		}
		for _, e := range g.endpoints {
			groupCmd.AddCommand(e.command(segment))
		}
		referenceCmd.AddCommand(groupCmd)
	}
}

// command builds one row of the table into a cobra command: its flags, and a
// RunE that turns them into a query.
func (e endpoint) command(group string) *cobra.Command {
	segment := e.path
	if segment == "" {
		segment = e.item
	}
	path := referencePrefix + group + "/" + segment

	// pageNum, as listFlags uses: the API rejects page=0 rather than treating
	// it as unset, so the flag refuses anything below 1 as it parses.
	var (
		search  string
		page    pageNum
		perPage pageNum
	)

	// One holder per param, keyed by the API's spelling, so RunE can find the
	// value again without repeating the switch on kind.
	strVal := make(map[string]*string)
	numVal := make(map[string]*int)
	strsVal := make(map[string]*[]string)
	numsVal := make(map[string]*[]int)

	cmd := &cobra.Command{
		Use:   e.item,
		Short: e.short,
		Args:  cobra.NoArgs,
	}
	if e.long != "" {
		cmd.Long = e.long
	}
	flags := cmd.Flags()

	if !e.noSearch {
		help := e.searchHelp
		if help == "" {
			help = "Case-insensitive contains match on the item label"
		}
		flags.StringVar(&search, "search", "", help)
	}
	if e.paged {
		flags.Var(&page, "page", "Page to fetch (default 1)")
		flags.Var(&perPage, "per-page",
			"Items per page (server default; capped at 500 on everything but the apps reads)")
	}

	for _, p := range e.params {
		name := flagName(p.name)
		switch p.kind {
		case str:
			v := new(string)
			strVal[p.name] = v
			flags.StringVar(v, name, "", p.help)
			if p.values != nil {
				completeValues(cmd, name, p.values)
			}
		case num:
			v := new(int)
			numVal[p.name] = v
			flags.IntVar(v, name, 0, p.help)
		case strs:
			v := new([]string)
			strsVal[p.name] = v
			// StringArray, not StringSlice: a value may contain a comma, and a
			// slice flag would split it into two bogus values.
			flags.StringArrayVar(v, name, nil, p.help+" (repeat the flag for more than one)")
		case nums:
			v := new([]int)
			numsVal[p.name] = v
			flags.IntSliceVar(v, name, nil, p.help+" (repeatable, or comma-separated)")
		}
		if p.required {
			// Caught locally rather than spent on a round trip that 422s.
			_ = cmd.MarkFlagRequired(name)
		}
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		// Refused rather than synthesised: --quiet prints one id per row
		// everywhere else, and a made-up "start-end" would still need
		// splitting, which --json and jq already do properly.
		if e.noID && quietOutput {
			return usageErr(cmd.CommandPath() + " returns rows with no id - --quiet does not apply, use --json")
		}

		if e.check != nil {
			if err := e.check(flags); err != nil {
				return err
			}
		}

		query := url.Values{}

		// Only flags the user set: an unset --per-page must not become
		// per_page=0, which the API rejects rather than ignores.
		if flags.Changed("search") {
			query.Set("search", search)
		}
		if flags.Changed("page") {
			query.Set("page", page.String())
		}
		if flags.Changed("per-page") {
			query.Set("per_page", perPage.String())
		}

		for _, p := range e.params {
			if !flags.Changed(flagName(p.name)) {
				continue
			}
			switch p.kind {
			case str:
				v := *strVal[p.name]
				if p.values != nil {
					if v = strings.ToLower(strings.TrimSpace(v)); !slices.Contains(p.values, v) {
						return usageErr("unknown --" + flagName(p.name) + " " + *strVal[p.name] +
							"; one of " + strings.Join(p.values, ", "))
					}
				}
				query.Set(p.name, v)
			case num:
				n := *numVal[p.name]
				if (p.min > 0 || p.max > 0) && (n < p.min || (p.max > 0 && n > p.max)) {
					want := strconv.Itoa(p.min) + " or more"
					if p.max > 0 {
						want = strconv.Itoa(p.min) + " to " + strconv.Itoa(p.max)
					}
					return usageErr("--" + flagName(p.name) + " " + strconv.Itoa(n) +
						" is out of range; " + want)
				}
				query.Set(p.name, strconv.Itoa(n))
			case strs:
				if p.oneKind {
					if err := oneKindOnly(flagName(p.name), *strsVal[p.name]); err != nil {
						return err
					}
				}
				for _, s := range *strsVal[p.name] {
					query.Add(p.name+"[]", s)
				}
			case nums:
				for _, n := range *numsVal[p.name] {
					query.Add(p.name+"[]", strconv.Itoa(n))
				}
			}
		}

		return runReferenceTable(cmd, path, query, e.table, e.detail, e.empty)
	}

	return cmd
}

// oneKindOnly refuses a list that mixes numeric ids with codes. The API
// decides from the first value which kind all of them are, as PHP's
// is_numeric does, and drops the others without a word.
func oneKindOnly(flag string, values []string) error {
	var ids, codes []string
	for _, v := range values {
		if _, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			ids = append(ids, v)
		} else {
			codes = append(codes, v)
		}
	}
	if len(ids) == 0 || len(codes) == 0 {
		return nil
	}
	return usageErr(fmt.Sprintf("--%s mixes ids (%s) and codes (%s); the API reads them all as "+
		"the kind of the first and drops the rest, so pass one kind", flag,
		strings.Join(ids, ", "), strings.Join(codes, ", ")))
}

// flagName: category_ids -> category-ids, datasetType -> dataset-type.
func flagName(param string) string {
	var b strings.Builder
	for i, r := range param {
		switch {
		case r == '_':
			b.WriteByte('-')
		case r >= 'A' && r <= 'Z':
			if i > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r + ('a' - 'A'))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// runReference performs the read: data on stdout, commentary on stderr, so a
// pipe only ever sees the payload.
//
// One request, one page. The biggest catalogs run to thousands of pages against
// a 120 requests/min budget, so callers ask for more with --page.
func runReference(cmd *cobra.Command, path string, query url.Values) error {
	return runReferenceTable(cmd, path, query, nil, nil, "")
}

// runReferenceTable is runReference with an optional shaper for the default
// table, or a detail view for a single record, for the reads whose rows the
// generic table prints badly.
func runReferenceTable(cmd *cobra.Command, path string, query url.Values,
	table func([]output.Record) ([]output.Record, []string),
	detail func([]output.Record) (output.Record, []string), empty string) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.GetRaw(cmd.Context(), path, query)
		if err != nil {
			printErrorEnvelope(cmd, err)
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	items, pg, err := api.ReadList[output.Record](cmd.Context(), c, path, query)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		msg := "no results"
		if empty != "" {
			msg += ": " + empty
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), msg)
		return nil
	}

	// output.ID reads value and id, covering every catalog shape. The footer
	// still prints: it is stderr commentary, and without it a paged catalog
	// truncates silently.
	if quietOutput {
		if err := output.IDs(cmd.OutOrStdout(), items); err != nil {
			return err
		}
		output.Footer(cmd.ErrOrStderr(), pg)
		return nil
	}

	if detail != nil {
		rec, lead := detail(items)
		if err := output.Detail(cmd.OutOrStdout(), rec, lead); err != nil {
			return err
		}
		output.Footer(cmd.ErrOrStderr(), pg)
		return nil
	}

	if table != nil {
		rows, cols := table(items)
		if err := output.TableWith(cmd.OutOrStdout(), rows, cols); err != nil {
			return err
		}
		output.Footer(cmd.ErrOrStderr(), pg)
		return nil
	}

	if err := output.Table(cmd.OutOrStdout(), inlineScalarLists(items)); err != nil {
		return err
	}
	output.Footer(cmd.ErrOrStderr(), pg)
	return nil
}

// inlineScalarLists joins each list of scalars into one cell, so a datastream's
// dataset_types reads "poi, competitors" rather than "[2 items]". Only that:
// unlike flatten it leaves {id, name} objects alone, and lists of objects stay
// counted - --json has them in full.
func inlineScalarLists(items []output.Record) []output.Record {
	rows := make([]output.Record, len(items))
	for i, item := range items {
		row := make(output.Record, len(item))
		for k, v := range item {
			if list, ok := v.([]any); ok {
				if s, ok := joinScalars(list); ok {
					row[k] = s
					continue
				}
			}
			row[k] = v
		}
		rows[i] = row
	}
	return rows
}
