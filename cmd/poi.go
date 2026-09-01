package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/output"
)

// My POI Data is your company's own places, not the shared POI catalog that
// 'intuizi reference poi' reads. The taxonomy is segments -> categories ->
// brands -> locations, and locations arrive through a submission.
//
// It does not use the resource table: these reads hang off /my-data/pois, the
// indexes take different parameters, and submissions have three create modes.

const poiPrefix = "/my-data/pois"

var poiCmd = &cobra.Command{
	Use:   "poi",
	Short: "Manage your own POI data",
	Long: `Manage your company's own points of interest.

The taxonomy nests: segments hold categories, categories hold brands, and
brands hold the locations you submit. Create the parent before the child.

Locations are not created one by one - they arrive in a submission, from a CSV
file, an upload reference, or an inline list. A submission is processed
asynchronously; watch it with 'intuizi poi submissions show <id>'.

This is your own data. The shared catalog every audience is built from is read
with 'intuizi reference poi'.`,
}

// --------------------------------------------------------------------------------- taxonomy reads

// searchList builds the three flat, search-only index reads.
func searchList(use, short, path, empty string, cols []string) *cobra.Command {
	var search string

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			query := url.Values{}
			if cmd.Flags().Changed("search") {
				query.Set("search", search)
			}
			return renderList(cmd, path, query, cols, empty)
		},
	}
	cmd.Flags().StringVar(&search, "search", "",
		"Case-insensitive contains match on the name")
	return cmd
}

// --------------------------------------------------------------------------------- brands / categories

func poiCreateCommand(use, short, path, parentFlag, parentHelp, long string, cols []string) *cobra.Command {
	var (
		name     string
		parentID int
	)

	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Long:  long,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			field := "category_id"
			if parentFlag == "segment-id" {
				field = "segment_id"
			}
			return createBody(cmd, path,
				map[string]any{"name": name, field: parentID}, cols, "")
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "The name")
	cmd.Flags().IntVar(&parentID, parentFlag, 0, parentHelp)
	_ = cmd.MarkFlagRequired("name")
	_ = cmd.MarkFlagRequired(parentFlag)
	return cmd
}

// --------------------------------------------------------------------------------- locations

func poiLocationsCommand() *cobra.Command {
	locations := &cobra.Command{
		Use:   "locations",
		Short: "Read your own POI locations",
	}

	var (
		search    string
		page      int
		perPage   int
		brands    []int
		countries []string
		geometry  string
	)

	list := &cobra.Command{
		Use:   "list",
		Short: "List your own POI locations",
		Long: `List your own POI locations.

--search matches across name, address, city, state, zip, DMA, external id and
placekey, so a store number finds its location as readily as a name.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			query := url.Values{}
			if flags.Changed("search") {
				query.Set("search", search)
			}
			if flags.Changed("page") {
				query.Set("page", strconv.Itoa(page))
			}
			if flags.Changed("per-page") {
				query.Set("per_page", strconv.Itoa(perPage))
			}
			for _, b := range brands {
				query.Add("brands[]", strconv.Itoa(b))
			}
			for _, c := range countries {
				query.Add("countries[]", c)
			}
			if flags.Changed("geometry") {
				if geometry != "polygon" && geometry != "coordinates" {
					return fmt.Errorf("--geometry must be polygon or coordinates, not %q", geometry)
				}
				query.Set("type", geometry)
			}
			return renderList(cmd, poiPrefix+"/index", query,
				[]string{"id", "name", "address1", "city", "state", "country"},
				"no locations")
		},
	}

	flags := list.Flags()
	flags.StringVar(&search, "search", "", "Match on name, address, city, state, zip, DMA, external or placekey id")
	flags.IntVar(&page, "page", 0, "Page to fetch (default 1)")
	flags.IntVar(&perPage, "per-page", 0, "Items per page (server default 25, capped at 500)")
	flags.IntSliceVar(&brands, "brands", nil, "Your own brand ids to filter by (repeatable, or comma-separated)")
	flags.StringArrayVar(&countries, "countries", nil, "Country codes to filter by (repeat the flag for more than one)")
	flags.StringVar(&geometry, "geometry", "", "Only locations stored as polygon or coordinates")

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one of your POI locations",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "location")
			if err != nil {
				return err
			}
			return renderOne(cmd, poiPrefix+"/"+strconv.Itoa(id),
				[]string{"id", "name", "address1", "city", "state", "country"})
		},
	}

	locations.AddCommand(list, show)
	return locations
}

// --------------------------------------------------------------------------------- submissions

var submissionColumns = []string{"id", "name", "status", "created_at"}

func poiSubmissionsCommand() *cobra.Command {
	subs := &cobra.Command{
		Use:   "submissions",
		Short: "Submit and track batches of POI locations",
		Long: `Submit and track batches of POI locations.

Three ways to send the same thing, differing only in where the locations come
from:

  create --file locations.csv        a CSV posted directly (up to 50 MB)
  create --upload-reference <ref>    a file already sent with 'intuizi uploads put'
  create --list locations.json       an inline JSON list, for a handful of places

All three take --name and --brand-id. Processing is asynchronous; follow it
with 'intuizi poi submissions show <id>'.`,
	}

	var (
		q       string
		sortBy  string
		orderBy string
	)

	list := &cobra.Command{
		Use:   "list",
		Short: "List your POI submissions",
		Long: `List your POI submissions.

This index is not paginated and takes its own parameters - the free-text filter
is --search, and results can be sorted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			query := url.Values{}
			// The API spells these q/sortBy/orderBy; the flags stay in the
			// CLI's own idiom.
			if flags.Changed("search") {
				query.Set("q", q)
			}
			if flags.Changed("sort-by") {
				switch sortBy {
				case "name", "status", "created_at", "updated_at":
					query.Set("sortBy", sortBy)
				default:
					return fmt.Errorf("--sort-by must be name, status, created_at or updated_at, not %q", sortBy)
				}
			}
			if flags.Changed("order") {
				switch orderBy {
				case "asc", "desc":
					query.Set("orderBy", orderBy)
				default:
					return fmt.Errorf("--order must be asc or desc, not %q", orderBy)
				}
			}
			return renderList(cmd, poiPrefix+"/submissions/index", query,
				submissionColumns, "no submissions")
		},
	}
	lf := list.Flags()
	lf.StringVar(&q, "search", "", "Free-text match on the submission name")
	lf.StringVar(&sortBy, "sort-by", "", "Sort by name, status, created_at or updated_at")
	lf.StringVar(&orderBy, "order", "", "Sort direction: asc or desc")

	show := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one POI submission",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "submission")
			if err != nil {
				return err
			}
			return renderOne(cmd, poiPrefix+"/submissions/"+strconv.Itoa(id), submissionColumns)
		},
	}

	del := poiSubmissionDeleteCommand()

	subs.AddCommand(list, show, poiSubmissionCreateCommand(), del)
	return subs
}

func poiSubmissionDeleteCommand() *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a waiting POI submission",
		Long: `Delete a POI submission that has not been processed yet.

Only a waiting submission can be deleted; once processing has started the
locations are already being ingested.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "submission")
			if err != nil {
				return err
			}
			if !yes {
				if err := confirm(cmd, fmt.Sprintf("Delete submission %d?", id)); err != nil {
					return err
				}
			}
			return postID(cmd, poiPrefix+"/submissions/delete-by-id", id,
				fmt.Sprintf("deleted submission %d", id))
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// poiSubmissionCreateCommand covers all three create routes behind one command,
// choosing the route from which source flag was given.
func poiSubmissionCreateCommand() *cobra.Command {
	var (
		name      string
		brandID   int
		file      string
		uploadRef string
		list      string
		update    bool
		remove    bool
		key       string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a POI submission from a file, an upload or a list",
		Long: `Create a POI submission.

Exactly one source:

    --file locations.csv          post the CSV directly (up to 50 MB)
    --upload-reference upl_...    claim a file sent with 'intuizi uploads put'
    --list locations.json         an inline JSON body carrying locations[]

--update and --remove need --key to say how existing POIs are matched.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()

			var sources int
			for _, f := range []string{"file", "upload-reference", "list"} {
				if flags.Changed(f) {
					sources++
				}
			}
			if sources != 1 {
				return errors.New("pass exactly one of --file, --upload-reference or --list")
			}
			if (update || remove) && key == "" {
				return errors.New("--update and --remove need --key to match on")
			}

			switch {
			case flags.Changed("list"):
				// The inline list is a whole JSON body; name and brand come
				// from the file unless the flags override them.
				payload, err := readPayload(cmd, list)
				if err != nil {
					return err
				}
				body, err := mergeSubmissionFields(payload, name, brandID, flags.Changed("name"), flags.Changed("brand-id"))
				if err != nil {
					return err
				}
				return createBody(cmd, poiPrefix+"/submissions/create-by-list", body,
					submissionColumns, "processing - run 'intuizi poi submissions show <id>'")

			case flags.Changed("upload-reference"):
				body := map[string]any{
					"name":             name,
					"brand_id":         brandID,
					"upload_reference": uploadRef,
				}
				addMatchFields(body, update, remove, key)
				return createBody(cmd, poiPrefix+"/submissions/create-by-upload", body,
					submissionColumns, "processing - run 'intuizi poi submissions show <id>'")

			default:
				return createSubmissionByFile(cmd, name, brandID, file, update, remove, key)
			}
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&name, "name", "", "The submission name")
	flags.IntVar(&brandID, "brand-id", 0, "The brand these locations belong to")
	flags.StringVar(&file, "file", "", "A CSV of locations in the Intuizi ingestion format")
	flags.StringVar(&uploadRef, "upload-reference", "", "An upload_reference from 'intuizi uploads put'")
	flags.StringVar(&list, "list", "", `A JSON body carrying locations[], or "-" for stdin`)
	flags.BoolVar(&update, "update", false, "Update existing matched POIs")
	flags.BoolVar(&remove, "remove", false, "Remove existing matched POIs")
	flags.StringVar(&key, "key", "",
		"How to match existing POIs: location-id, gps-coordinates, store-id, master-id or external-id")

	return cmd
}

func addMatchFields(body map[string]any, update, remove bool, key string) {
	if update {
		body["update"] = true
	}
	if remove {
		body["remove"] = true
	}
	if key != "" {
		body["key"] = key
	}
}

// mergeSubmissionFields lets --name and --brand-id override what the list file
// carries, so one file can be reused across brands.
func mergeSubmissionFields(payload []byte, name string, brandID int, setName, setBrand bool) (map[string]any, error) {
	body, err := decodeObject(payload)
	if err != nil {
		return nil, err
	}
	if setName {
		body["name"] = name
	}
	if setBrand {
		body["brand_id"] = brandID
	}
	if _, ok := body["name"]; !ok {
		return nil, errors.New("the list file has no name - pass --name")
	}
	if _, ok := body["brand_id"]; !ok {
		return nil, errors.New("the list file has no brand_id - pass --brand-id")
	}
	return body, nil
}

func init() {
	poiCmd.AddCommand(
		searchList("segments", "List your own POI segments",
			poiPrefix+"/segments/index", "no segments", []string{"id", "name"}),

		poiGroup("categories", "Your own POI categories",
			searchList("list", "List your own POI categories",
				poiPrefix+"/categories/index", "no categories", []string{"id", "name"}),
			poiCreateCommand("create", "Create a POI category",
				poiPrefix+"/categories/create", "segment-id",
				"The parent segment id", `Create a POI category under one of your segments.

Read the parent ids first with 'intuizi poi segments'.`, []string{"id", "name"})),

		poiGroup("brands", "Your own POI brands",
			searchList("list", "List your own POI brands",
				poiPrefix+"/brands/index", "no brands", []string{"id", "name"}),
			poiCreateCommand("create", "Create a POI brand",
				poiPrefix+"/brands/create", "category-id",
				"The parent category id", `Create a POI brand under one of your categories.

Read the parent ids first with 'intuizi poi categories list'.`, []string{"id", "name"})),

		poiLocationsCommand(),
		poiSubmissionsCommand(),
	)
	rootCmd.AddCommand(poiCmd)
}

// poiGroup wraps a list/create pair under one noun.
func poiGroup(use, short string, subs ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{Use: use, Short: short}
	g.AddCommand(subs...)
	return g
}

// decodeObject is readPayload's result as a mutable map, for the one create
// that merges flags into a caller-supplied body.
func decodeObject(payload []byte) (map[string]any, error) {
	var body map[string]any
	if err := unmarshalRecord(payload, &body); err != nil {
		return nil, err
	}
	return body, nil
}

// unmarshalRecord keeps large ids exact, the same way output.Record does.
func unmarshalRecord(payload []byte, out *map[string]any) error {
	var rec output.Record
	if err := rec.UnmarshalJSON(payload); err != nil {
		return fmt.Errorf("decoding the list payload: %w", err)
	}
	*out = rec
	return nil
}

// createSubmissionByFile posts the CSV itself as multipart/form-data. Boolean
// form fields go as "1"/"0" - the string "true" is rejected.
func createSubmissionByFile(cmd *cobra.Command, name string, brandID int, file string,
	update, remove bool, key string) error {

	c, err := client()
	if err != nil {
		return err
	}

	fields := map[string]string{
		"name":     name,
		"brand_id": strconv.Itoa(brandID),
	}
	if update {
		fields["update"] = "1"
	}
	if remove {
		fields["remove"] = "1"
	}
	if key != "" {
		fields["key"] = key
	}

	var created output.Record
	if err := c.PostMultipart(cmd.Context(), poiPrefix+"/submissions/create-by-file",
		fields, "locations_file", file, &created); err != nil {
		return err
	}

	if err := output.Detail(cmd.OutOrStdout(), flatten(created), submissionColumns); err != nil {
		return err
	}
	fmt.Fprintln(cmd.ErrOrStderr(), "processing - run 'intuizi poi submissions show <id>'")
	return nil
}
