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

	var (
		search  string
		page    int
		perPage int
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
	flags := cmd.Flags()

	if !e.noSearch {
		flags.StringVar(&search, "search", "",
			"Case-insensitive contains match on the item label")
	}
	if e.paged {
		flags.IntVar(&page, "page", 0, "Page to fetch (default 1)")
		flags.IntVar(&perPage, "per-page", 0,
			"Items per page (server default; capped at 500 on everything but the apps reads)")
	}

	for _, p := range e.params {
		name := flagName(p.name)
		switch p.kind {
		case str:
			v := new(string)
			strVal[p.name] = v
			flags.StringVar(v, name, "", p.help)
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
		query := url.Values{}

		// Only flags the user set: an unset --per-page must not become
		// per_page=0, which the API rejects rather than ignores.
		if flags.Changed("search") {
			query.Set("search", search)
		}
		if flags.Changed("page") {
			query.Set("page", strconv.Itoa(page))
		}
		if flags.Changed("per-page") {
			query.Set("per_page", strconv.Itoa(perPage))
		}

		for _, p := range e.params {
			if !flags.Changed(flagName(p.name)) {
				continue
			}
			switch p.kind {
			case str:
				query.Set(p.name, *strVal[p.name])
			case num:
				query.Set(p.name, strconv.Itoa(*numVal[p.name]))
			case strs:
				for _, s := range *strsVal[p.name] {
					query.Add(p.name+"[]", s)
				}
			case nums:
				for _, n := range *numsVal[p.name] {
					query.Add(p.name+"[]", strconv.Itoa(n))
				}
			}
		}

		return runReference(cmd, path, query)
	}

	return cmd
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
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.GetRaw(cmd.Context(), path, query)
		if err != nil {
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	items, pg, err := api.ReadList[output.Record](cmd.Context(), c, path, query)
	if err != nil {
		return err
	}

	if len(items) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "no results")
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

	if err := output.Table(cmd.OutOrStdout(), items); err != nil {
		return err
	}
	output.Footer(cmd.ErrOrStderr(), pg)
	return nil
}
