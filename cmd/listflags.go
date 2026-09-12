package cmd

import (
	"errors"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"
)

// pageNum is a --page or --per-page value. The API rejects 0 rather than
// treating it as unset, so the flag refuses anything below 1 as it parses, the
// way it already refuses "abc": cobra reports both before running anything,
// and both exit 2. Done here because listFlags' callers only ever see the
// url.Values, and its signature is shared.
type pageNum int

func (p *pageNum) String() string { return strconv.Itoa(int(*p)) }
func (p *pageNum) Type() string   { return "int" }

func (p *pageNum) Set(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	if n < 1 {
		return errors.New("must be 1 or more")
	}
	*p = pageNum(n)
	return nil
}

// listFlags registers the pagination flags every index read takes and returns
// the builder that reads them back. The flag variables stay captive in the
// closure, so two commands can never collide on them, and the rule that an
// unset flag is not sent lives in exactly one place.
//
// Pass an empty searchHelp for an index that takes no ?search.
func listFlags(cmd *cobra.Command, searchHelp string) func() url.Values {
	var (
		search  string
		page    pageNum
		perPage pageNum
	)

	flags := cmd.Flags()
	if searchHelp != "" {
		flags.StringVar(&search, "search", "", searchHelp)
	}
	flags.Var(&page, "page", "Page to fetch (default 1)")
	flags.Var(&perPage, "per-page", "Items per page (server default 25, capped at 100)")

	return func() url.Values {
		q := url.Values{}
		// An unset --per-page must not become per_page=0, which the API
		// rejects rather than ignores.
		if searchHelp != "" && flags.Changed("search") {
			q.Set("search", search)
		}
		if flags.Changed("page") {
			q.Set("page", page.String())
		}
		if flags.Changed("per-page") {
			q.Set("per_page", perPage.String())
		}
		return q
	}
}
