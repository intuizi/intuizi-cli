package cmd

import (
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// listFlags registers the pagination flags every index read takes and returns
// the builder that reads them back. The flag variables stay captive in the
// closure, so two commands can never collide on them, and the rule that an
// unset flag is not sent lives in exactly one place.
//
// Pass an empty searchHelp for an index that takes no ?search.
func listFlags(cmd *cobra.Command, searchHelp string) func() url.Values {
	var (
		search  string
		page    int
		perPage int
	)

	flags := cmd.Flags()
	if searchHelp != "" {
		flags.StringVar(&search, "search", "", searchHelp)
	}
	flags.IntVar(&page, "page", 0, "Page to fetch (default 1)")
	flags.IntVar(&perPage, "per-page", 0, "Items per page (server default 25, capped at 100)")

	return func() url.Values {
		q := url.Values{}
		// An unset --per-page must not become per_page=0, which the API
		// rejects rather than ignores.
		if searchHelp != "" && flags.Changed("search") {
			q.Set("search", search)
		}
		if flags.Changed("page") {
			q.Set("page", strconv.Itoa(page))
		}
		if flags.Changed("per-page") {
			q.Set("per_page", strconv.Itoa(perPage))
		}
		return q
	}
}
