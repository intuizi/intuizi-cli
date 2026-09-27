package cmd

import (
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// My POI Data is your company's own places, not the shared POI catalog that
// 'intuizi reference poi' reads. The taxonomy is segments -> categories ->
// brands -> locations: segments are a fixed list to pick from, categories and
// brands are the company's own, and locations arrive through a submission.
//
// It does not use the resource table: these reads hang off /my-data/pois, the
// indexes take different parameters, and submissions have three create modes.

const poiPrefix = "/my-data/pois"

var poiCmd = &cobra.Command{
	Use:   "poi",
	Short: "Manage your own POI data",
	Long: `Manage your company's own points of interest.

The taxonomy nests: segments hold categories, categories hold brands, and
brands hold the locations you submit. Segments already exist and cannot be
created: pick one with 'intuizi poi segments list', create categories under
it, and brands under those.

Locations are not created one by one - they arrive in a submission, from a CSV
file, an upload reference, or a JSON file of locations. A submission is
processed asynchronously; watch it with 'intuizi poi submissions show <id>'.

This is your own data. The shared catalog every audience is built from is read
with 'intuizi reference poi'.`,
}

// --------------------------------------------------------------------------------- taxonomy reads

// searchList builds the three flat, search-only index reads. They answer in the
// catalog idiom - {value, text} - not the {id, name} the locations and
// submissions reads use, so the value is the id to pass back as a parent.
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
			// MarkFlagRequired checks presence, not content; "" and 0 both
			// pass it and earn a 422.
			if name == "" {
				return usageErr("--name must not be empty")
			}
			if parentID <= 0 {
				return usageErr(fmt.Sprintf("--%s must be a positive id, not %d", parentFlag, parentID))
			}
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
		page      pageNum // refuses 0 as it parses, like every other index
		perPage   pageNum
		brands    []int
		countries []string
		geometry  string
	)

	list := &cobra.Command{
		Use:   "list",
		Short: "List your own POI locations",
		Long: `List your own POI locations.

--search matches across name, address, city, state, zip, DMA, external id and
placekey. It does not match store_id, so a store number is found only when it
is also in one of those fields.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := cmd.Flags()
			query := url.Values{}
			if flags.Changed("search") {
				query.Set("search", search)
			}
			if flags.Changed("page") {
				query.Set("page", page.String())
			}
			if flags.Changed("per-page") {
				query.Set("per_page", perPage.String())
			}
			for _, b := range brands {
				query.Add("brands[]", strconv.Itoa(b))
			}
			for _, c := range countries {
				query.Add("countries[]", c)
			}
			if flags.Changed("geometry") {
				if geometry != "polygon" && geometry != "coordinates" {
					return usageErr(fmt.Sprintf("--geometry must be polygon or coordinates, not %q", geometry))
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
	flags.Var(&page, "page", "Page to fetch (default 1)")
	flags.Var(&perPage, "per-page", "Items per page (server default 25, capped at 500)")
	flags.IntSliceVar(&brands, "brands", nil, "Your own brand ids to filter by (repeatable, or comma-separated)")
	flags.StringArrayVar(&countries, "countries", nil,
		"Country codes to filter by, ISO-3 (e.g. USA); locations are stored as\n"+
			"ISO-3 even when submitted as alpha-2, so US matches nothing\n"+
			"(repeat the flag for more than one)")
	flags.StringVar(&geometry, "geometry", "", "Filter by how the location is stored: polygon or coordinates")

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
  create --upload-reference <ref>    a file already sent with
                                     'intuizi uploads put --purpose poi_submission'
  create --list locations.json       a JSON file of locations, or - for stdin,
                                     for a handful of places

All three take --name and --brand-id; with --list they may come from the file
instead. Processing is asynchronous; follow it with
'intuizi poi submissions show <id>'.`,
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
					return usageErr(fmt.Sprintf("--sort-by must be name, status, created_at or updated_at, not %q", sortBy))
				}
			}
			if flags.Changed("order") {
				switch orderBy {
				case "asc", "desc":
					query.Set("orderBy", orderBy)
				default:
					return usageErr(fmt.Sprintf("--order must be asc or desc, not %q", orderBy))
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
		Long: `Delete a POI submission whose status is Waiting.

A new submission starts as Importing and reaches Waiting once Intuizi has read
its locations, so a delete sent straight after the create is refused: check
'intuizi poi submissions show <id>' first. An imported or disabled submission
cannot be deleted.`,
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
    --upload-reference upl_...    claim a file sent with
                                  'intuizi uploads put --purpose poi_submission'
    --list locations.json         a JSON file holding locations[], or - to read
                                  it from stdin

With --list, the file's name and brand_id are sent unless --name and
--brand-id override them, and --key, --update and --remove replace the
file's key, update and remove (--update=false and --remove=false turn off
a true in the file). stderr notes each flag that replaces a different
value in the file.

Each listed location is matched to the brand's existing POIs on --key
(the API uses gps-coordinates when it is omitted, or null in a --list body).
A location that matches is never added a second time: --update changes the
POI it matches, and without --update that POI is left as it is. A location
that matches nothing is added as a new POI.

--remove keeps the POIs the listed locations match and archives every other
POI the brand had. It is applied once, over the whole submission, so list
every location the brand should keep, in one submission, not the ones to
drop. A --remove submission whose locations carry values for --key but match
no POI archives every POI the brand had. One that holds no location at all,
or in which no location has a value for --key (a location_id, store_id,
master_id or external_id, a blank value counting as none), archives nothing:
its locations are still added. --update and --remove take effect when
Intuizi approves the submission, and both need --key.

With --key location-id, a location matches the brand's POI whose id is its
location_id, the id column of 'intuizi poi locations list'. A location_id
must be the id of one of your POIs, or the API rejects the submission (an
--upload-reference file is checked only in its first 64 KB). A location
with no location_id, or one that is not the id of one of the brand's POIs,
is added as a new POI.

Only the --upload-reference form sends an Idempotency-Key, so only it can be
retried safely with --idempotency-key; the API reads none on the --file and
--list forms, and the CLI says the flag has no effect there.`,
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
				return usageErr("pass exactly one of --file, --upload-reference or --list")
			}
			if (update || remove) && key == "" {
				return usageErr("--update and --remove need --key to match on")
			}
			// Changed, not key != "": an empty --key would otherwise be
			// dropped as if it had not been given.
			if flags.Changed("key") && !matchKeys[key] {
				return usageErr(fmt.Sprintf("--key must be %s, not %q", matchKeyList, key))
			}
			if flags.Changed("file") {
				// Streaming a CSV from stdin into multipart is not worth the
				// complexity while --list already reads a pipe.
				if file == "-" {
					return usageErr("--file does not read stdin - pipe a JSON body to --list - instead")
				}
				// Checked before the file is opened: the server accepts .csv
				// and .txt, and anything else is a 422 after a 50 MB upload.
				switch strings.ToLower(filepath.Ext(file)) {
				case ".csv", ".txt":
				default:
					return usageErr(fmt.Sprintf("--file must be a .csv or .txt file, not %s", file))
				}
			}
			// Not MarkFlagRequired: cobra applies that to every branch before
			// RunE, and --list legitimately takes both from the file.
			if !flags.Changed("list") {
				var missing []string
				if name == "" {
					missing = append(missing, "--name")
				}
				if brandID <= 0 {
					missing = append(missing, "--brand-id")
				}
				if len(missing) > 0 {
					return usageErr("pass " + strings.Join(missing, " and "))
				}
			}

			switch {
			case flags.Changed("list"):
				// The inline list is a whole JSON body; name and brand come
				// from the file unless the flags override them.
				payload, err := readPayload(cmd, list)
				if err != nil {
					return err
				}
				stderr := cmd.ErrOrStderr()
				body, err := mergeSubmissionFields(stderr, payload, name, brandID,
					flags.Changed("name"), flags.Changed("brand-id"))
				if err != nil {
					return err
				}
				// The match flags replace the body's fields the same way,
				// decided by Changed so --remove=false turns off a remove:true
				// in a reused file. JSON like create-by-upload, so the
				// booleans are real ones.
				for _, f := range []struct {
					flag  string
					value any
				}{{"update", update}, {"remove", remove}, {"key", key}} {
					if flags.Changed(f.flag) {
						noteOverride(stderr, body, f.flag, f.value)
						body[f.flag] = f.value
					}
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
	flags.StringVar(&uploadRef, "upload-reference", "",
		"An upload_reference from 'intuizi uploads put --purpose poi_submission'")
	flags.StringVar(&list, "list", "",
		"Path to a JSON file holding locations[] (and name and brand_id\n"+
			`unless the flags give them), or "-" to read it from stdin`)
	flags.BoolVar(&update, "update", false, "Update the brand's existing POIs that a listed location matches")
	flags.BoolVar(&remove, "remove", false,
		"Archive the brand's existing POIs that no listed location matches (matched\n"+
			"ones are kept), once, over the whole submission, when Intuizi approves it;\n"+
			"not applied when no location has a value for --key")
	flags.StringVar(&key, "key", "",
		"How listed locations are matched to the brand's existing POIs:\n"+
			matchKeyList+"\n(the API uses gps-coordinates when omitted, or null in a --list body)")

	return cmd
}

// matchKeys is the closed set --key takes; the server validates it too, but a
// typo should fail here rather than after the upload.
var matchKeys = map[string]bool{
	"location-id": true, "gps-coordinates": true, "store-id": true, "master-id": true, "external-id": true,
}

const matchKeyList = "location-id, gps-coordinates, store-id, master-id or external-id"

// addMatchFields writes the match flags into a body that starts empty (the
// --upload-reference form), so an unset or false flag leaves the API default.
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
// carries, so one file can be reused across brands. An override of a different
// value is noted on w.
func mergeSubmissionFields(w io.Writer, payload []byte, name string, brandID int, setName, setBrand bool) (map[string]any, error) {
	body, err := decodeObject(payload)
	if err != nil {
		return nil, err
	}
	if setName {
		noteOverride(w, body, "name", name)
		body["name"] = name
	}
	if setBrand {
		noteOverride(w, body, "brand-id", brandID)
		body["brand_id"] = brandID
	}
	if _, ok := body["name"]; !ok {
		return nil, usageErr("the list file has no name - pass --name")
	}
	if _, ok := body["brand_id"]; !ok {
		return nil, usageErr("the list file has no brand_id - pass --brand-id")
	}
	return body, nil
}

// noteOverride says on w when the flag for field is about to replace a
// different value the --list body already carries. The flag wins, which is
// the point, but a silent swap would hide a file that names another brand.
// flag is the flag's name, which is the field's with - for _.
func noteOverride(w io.Writer, body map[string]any, flag string, value any) {
	field := strings.ReplaceAll(flag, "-", "_")
	old, ok := body[field]
	// Compared as text: the body's numbers decode as json.Number.
	if !ok || fmt.Sprint(old) == fmt.Sprint(value) {
		return
	}
	given := "--" + flag
	switch v := value.(type) {
	case bool:
		// A bare --remove reads as true; false has to say so.
		if !v {
			given += "=false"
		}
	default:
		given += " " + shown(value)
	}
	_, _ = fmt.Fprintf(w, "note: %s replaces %s %s from the --list body\n", given, field, shown(old))
}

// shown quotes a string so an empty or spaced name reads as one value.
func shown(v any) string {
	if s, ok := v.(string); ok {
		return strconv.Quote(s)
	}
	return fmt.Sprint(v)
}

func init() {
	poiCmd.AddCommand(
		poiGroup("segments", "The available POI segments",
			searchList("list", "List the available POI segments",
				poiPrefix+"/segments/index", "no segments", []string{"value", "text"})),

		poiGroup("categories", "Your own POI categories",
			searchList("list", "List your own POI categories",
				poiPrefix+"/categories/index", "no categories", []string{"value", "text"}),
			poiCreateCommand("create", "Create a POI category",
				poiPrefix+"/categories/create", "segment-id",
				"The parent segment id", `Create a POI category under one of the available segments.

Read the parent ids first with 'intuizi poi segments list'.`, []string{"id", "name"})),

		poiGroup("brands", "Your own POI brands",
			searchList("list", "List your own POI brands",
				poiPrefix+"/brands/index", "no brands", []string{"value", "text"}),
			poiCreateCommand("create", "Create a POI brand",
				poiPrefix+"/brands/create", "category-id",
				"The parent category id", `Create a POI brand under one of your categories.

Read the parent ids first with 'intuizi poi categories list'.`, []string{"id", "name"})),

		poiLocationsCommand(),
		poiSubmissionsCommand(),
	)
	rootCmd.AddCommand(poiCmd)
}

// poiGroup wraps a noun's subcommands - list, and create where there is one.
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

	if jsonOutput {
		raw, err := api.CreateMultipartRaw(cmd.Context(), c,
			poiPrefix+"/submissions/create-by-file", fields, "locations_file", file)
		if err != nil {
			printErrorEnvelope(cmd, err)
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	created, err := api.CreateMultipart[output.Record](cmd.Context(), c,
		poiPrefix+"/submissions/create-by-file", fields, "locations_file", file)
	if err != nil {
		return err
	}

	if quietOutput {
		return output.IDs(cmd.OutOrStdout(), []output.Record{created})
	}
	if err := output.Detail(cmd.OutOrStdout(), flatten(created), submissionColumns); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "processing - run 'intuizi poi submissions show <id>'")
	return nil
}
