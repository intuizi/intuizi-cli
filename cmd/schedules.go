package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	// The static binary must resolve --timezone on hosts with no tz database.
	_ "time/tzdata"

	"github.com/spf13/cobra"

	"github.com/intuizi/intuizi-cli/internal/output"
)

const schedulesPrefix = "/analyses/schedules"

var scheduleColumns = []string{"id", "name", "status"}

// scheduleDetail leads the single-record views; the index has no project.
var scheduleDetail = []string{"id", "name", "status", "project"}

// startLayout is the server's date_format for recurrence.start.
const startLayout = "2006-01-02 15:04:05"

var schedulesCmd = &cobra.Command{
	Use:   "schedules",
	Short: "Manage schedules",
	Long: `Manage schedules.

A schedule rebuilds a completed audience on a recurring data window, and can
re-export it every cycle. The audience definition is snapshotted when the
schedule is created, so later edits to the original do not change it.

Schedules require additional permissions which need to be approved by your
Account Manager. Every schedules command needs them, list and show included;
a 403 means they are not enabled for the account.

Each cycle produces its own audience, named "<name> - #<cycle>".

The cadence values a schedule accepts come from 'intuizi reference':
common schedule-frequencies, schedule-windows and schedule-endings.`,
}

// --------------------------------------------------------------------------------- create

// scheduleEnding: 1 Never carries nothing, 2 after_recurrences, 3 end_date.
type scheduleEnding struct {
	Type             int    `json:"type"`
	AfterRecurrences int    `json:"after_recurrences,omitempty"`
	EndDate          string `json:"end_date,omitempty"`
}

// scheduleRecurrence: window 3 Custom is the only one needing window_days.
type scheduleRecurrence struct {
	Start      string         `json:"start"`
	Timezone   string         `json:"timezone"`
	Frequency  string         `json:"frequency"`
	WindowType int            `json:"window_type"`
	WindowDays int            `json:"window_days,omitempty"`
	Ending     scheduleEnding `json:"ending"`
}

type scheduleBody struct {
	Name       string             `json:"name"`
	AudienceID int                `json:"audience_id"`
	ProjectID  int                `json:"project_id,omitempty"` // 0 is never valid, so unset is left out
	Recurrence scheduleRecurrence `json:"recurrence"`
}

// scheduleFields are the body-building flags, for --file to reject.
var scheduleFields = []string{
	"name", "audience-id", "project-id", "start", "timezone", "frequency",
	"window", "window-days", "ending", "after-recurrences", "end-date",
}

var scheduleRequired = []string{"name", "audience-id", "start", "timezone", "frequency", "window"}

// Mirrors the server rule, so a rejected name costs no round trip.
var scheduleName = regexp.MustCompile(`^[a-zA-Z0-9_\- ]+$`)

// apiZoneRegions are the areas PHP files its current zone names under. The
// API validates --timezone with Laravel's timezone:all, which is PHP's
// DateTimeZone::ALL list: UTC and the names under these, and no
// backward-compatible ones. So US/Eastern, GMT, EST5EDT and every Etc/ name,
// all of which Go loads, are sure 422s. A legacy link filed under a region,
// such as Asia/Calcutta for Asia/Kolkata, still passes here: telling it apart
// needs PHP's own list.
var apiZoneRegions = []string{
	"Africa/", "America/", "Antarctica/", "Arctic/", "Asia/", "Atlantic/",
	"Australia/", "Europe/", "Indian/", "Pacific/",
}

// apiZoneName reports whether the API's zone list can hold tz: UTC, or a name
// under one of apiZoneRegions.
func apiZoneName(tz string) bool {
	if tz == "UTC" {
		return true
	}
	for _, r := range apiZoneRegions {
		if strings.HasPrefix(tz, r) {
			return true
		}
	}
	return false
}

// scheduleFrequencies are the catalog's values: lowercase, unlike the labels
// the console shows.
var scheduleFrequencies = map[string]string{
	"daily": "daily", "weekly": "weekly", "bi-weekly": "bi-weekly", "monthly": "monthly",
}

func schedulesCreateCommand() *cobra.Command {
	var (
		file       string
		dryRun     bool
		name       string
		audienceID int
		projectID  int
		start      string
		timezone   string
		frequency  string
		window     int
		windowDays int
		ending     int
		after      int
		endDate    string
	)

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a schedule, from flags or a payload file",
		Long: `Create a schedule.

The recurrence block is one level deep, so it can be built from flags:

    intuizi schedules create --name "Weekly coffee refresh" --audience-id 88 \
      --start "2027-09-15 06:00:00" --timezone America/New_York \
      --frequency weekly --window 4

--frequency, --window and --ending take values from the catalogs:

    intuizi reference common schedule-frequencies
    intuizi reference common schedule-windows
    intuizi reference common schedule-endings

--timezone takes a region-based IANA name such as America/New_York or
Asia/Kolkata, or UTC. The API rejects legacy and Etc/ names. US/Eastern, GMT
or Etc/UTC is refused here before anything is sent, but a legacy name filed
under a region, such as Asia/Calcutta, passes this check, --dry-run
included, and the API rejects it with a 422.

--start must be in the future and is read in --timezone. --window 3 is Custom
and also needs --window-days. --ending defaults to 1 Never; 2 Recurrences needs
--after-recurrences and 3 Custom Date needs --end-date, on or after the
--start date.

An activation block for auto-export is nested, so a schedule that exports every
cycle is passed whole instead:

    intuizi schedules create --file schedule.json

The request carries an Idempotency-Key, and a retry after a 429 reuses it.
Running the command again sends a fresh key and can create a second schedule.
When a create gets no response at all, stderr prints the key it used: rerun
with --idempotency-key <key> to retry it without risking a duplicate.`,
		Args: cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		flags := cmd.Flags()
		path := schedulesPrefix + "/create"

		if file != "" {
			if err := rejectBodyFlags(flags, scheduleFields); err != nil {
				return err
			}
			if dryRun {
				return usageErr("--dry-run builds a body from flags; --file already has one")
			}
			return createFromFile(cmd, path, file, scheduleDetail, "")
		}

		if err := missingFlags(flags, scheduleRequired, "a schedule"); err != nil {
			return err
		}
		if err := positiveID(flags, "audience-id", audienceID); err != nil {
			return err
		}
		if err := positiveID(flags, "project-id", projectID); err != nil {
			return err
		}

		// Spaces alone pass the regex, but the server's required rule trims them.
		if err := nonEmpty("name", name); err != nil {
			return err
		}
		if !scheduleName.MatchString(name) {
			return usageErr(fmt.Sprintf(
				"--name %q may use only letters, digits, spaces, _ and -", name))
		}
		// The API's cap; the characters above are ASCII, so bytes are characters.
		if len(name) > 255 {
			return usageErr(fmt.Sprintf("--name is %d characters; the most a schedule name takes is 255", len(name)))
		}

		freq, ok := scheduleFrequencies[strings.ToLower(frequency)]
		if !ok {
			return usageErr("unknown --frequency " + frequency +
				"; one of bi-weekly, daily, monthly, weekly")
		}

		// LoadLocation takes "" and "Local" as this machine's zone; the server
		// takes neither, and a schedule's zone should not depend on where the
		// CLI ran.
		if timezone == "" || timezone == "Local" {
			return usageErr(fmt.Sprintf("--timezone %q is not an IANA zone; use one like America/New_York", timezone))
		}
		loc, err := time.LoadLocation(timezone)
		if err != nil {
			return usageErr("unknown --timezone " + timezone + "; use an IANA name like America/New_York")
		}
		if !apiZoneName(timezone) {
			return usageErr("--timezone " + timezone + " is a legacy or Etc/ name, which the API " +
				"rejects; use UTC or a region-based name such as America/New_York")
		}
		startAt, err := time.ParseInLocation(startLayout, start, loc)
		if err != nil {
			return usageErr(fmt.Sprintf(`--start must be "YYYY-MM-DD HH:MM:SS", not %q`, start))
		}
		if !startAt.After(time.Now()) {
			return usageErr("--start " + start + " must be in the future in " + timezone)
		}

		// Each rule carries its own field, and the API answers the wrong one
		// with a 422 (prohibited_unless); checked here to name the mistake
		// without a round trip. An explicit 0 is a mistake to name too, so
		// these go by Changed, not value.
		switch ending {
		case 1:
			if flags.Changed("after-recurrences") || flags.Changed("end-date") {
				return usageErr("--ending 1 is Never; drop --after-recurrences and --end-date")
			}
		case 2:
			if !flags.Changed("after-recurrences") {
				return usageErr("--ending 2 is Recurrences and needs --after-recurrences")
			}
			if after < 1 {
				return usageErr("--after-recurrences must be at least 1, not " + strconv.Itoa(after))
			}
			if flags.Changed("end-date") {
				return usageErr("--end-date belongs to --ending 3, not 2")
			}
		case 3:
			if !flags.Changed("end-date") {
				return usageErr("--ending 3 is Custom Date and needs --end-date")
			}
			end, err := time.Parse(dateLayout, endDate)
			if err != nil {
				return usageErr("--end-date must be YYYY-MM-DD, not " + endDate)
			}
			// Neither the API nor the run count rejects an earlier end date:
			// the gap is counted forward from --start, so the schedule would
			// run for that many days instead of not at all.
			if first := startAt.Format(dateLayout); end.Format(dateLayout) < first {
				return usageErr(fmt.Sprintf("--end-date %s is before the --start date %s; "+
					"the schedule ends on or after the day it starts", endDate, first))
			}
			if flags.Changed("after-recurrences") {
				return usageErr("--after-recurrences belongs to --ending 2, not 3")
			}
		default:
			return usageErr("unknown --ending " + strconv.Itoa(ending) +
				"; 1 Never, 2 Recurrences or 3 Custom Date")
		}

		if window < 1 || window > 8 {
			return usageErr("--window " + strconv.Itoa(window) +
				" is not a data window id; 1 to 8, from 'reference common schedule-windows'")
		}
		if window == 3 {
			if !flags.Changed("window-days") {
				return usageErr("--window 3 is Custom and needs --window-days")
			}
			if windowDays < 1 || windowDays > 365 {
				return usageErr("--window-days must be 1 to 365, not " + strconv.Itoa(windowDays))
			}
		} else if flags.Changed("window-days") {
			return usageErr("--window-days belongs to --window 3 Custom")
		}

		body := scheduleBody{
			Name:       name,
			AudienceID: audienceID,
			ProjectID:  projectID,
			Recurrence: scheduleRecurrence{
				Start:      startAt.Format(startLayout),
				Timezone:   timezone,
				Frequency:  freq,
				WindowType: window,
				WindowDays: windowDays,
				Ending: scheduleEnding{
					Type: ending, AfterRecurrences: after, EndDate: endDate,
				},
			},
		}

		if dryRun {
			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			return enc.Encode(body)
		}
		return createBody(cmd, path, body, scheduleDetail, "")
	}

	f := cmd.Flags()
	f.StringVar(&file, "file", "",
		`Path to the schedule payload, or "-" to read it from stdin`)
	f.BoolVar(&dryRun, "dry-run", false,
		"Print the body the flags produce and send nothing")
	f.StringVar(&name, "name", "", "Name for the schedule")
	f.IntVar(&audienceID, "audience-id", 0, "The audience to rebuild each cycle")
	f.IntVar(&projectID, "project-id", 0, "Project to file the schedule under")
	f.StringVar(&start, "start", "",
		`First run, "YYYY-MM-DD HH:MM:SS", read in --timezone; must be in the future`)
	f.StringVar(&timezone, "timezone", "",
		"Region-based IANA zone such as America/New_York, or UTC; the API\n"+
			"rejects legacy names (US/Eastern, Asia/Calcutta) and Etc/ names")
	f.StringVar(&frequency, "frequency", "",
		"daily, weekly, bi-weekly or monthly (case-insensitive)")
	f.IntVar(&window, "window", 0,
		"Data window id from 'reference common schedule-windows'")
	f.IntVar(&windowDays, "window-days", 0, "Day count for --window 3 Custom")
	f.IntVar(&ending, "ending", 1,
		"Stop rule from 'reference common schedule-endings': 1 Never, 2 Recurrences, 3 Custom Date")
	f.IntVar(&after, "after-recurrences", 0, "Number of runs before stopping (--ending 2)")

	completeValues(cmd, "frequency", sortedKeys(scheduleFrequencies))
	f.StringVar(&endDate, "end-date", "",
		"Date the schedule ends, YYYY-MM-DD (--ending 3), on or after the --start\n"+
			"date; counted as whole cycles from --start to 00:00 on that date, so it\n"+
			"is not always a run date")
	return cmd
}

// --------------------------------------------------------------------------------- activate / deactivate

// toggleCommand builds activate and deactivate, which differ only in their verb.
func toggleCommand(verb, path, done, long string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <id>",
		Short: fmt.Sprintf("%s a schedule", cases(verb)),
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], "schedule")
			if err != nil {
				return err
			}
			return postID(cmd, schedulesPrefix+"/"+path, id,
				fmt.Sprintf("%s schedule %d", done, id))
		},
	}
}

// schedulesActivateCommand is toggleCommand's activate, plus a look at the
// schedule it returns: activating one whose ending was already met sets it
// Active but arms no further run, and nothing in the response says so.
func schedulesActivateCommand() *cobra.Command {
	cmd := toggleCommand("activate", "activate", "activated",
		`Activate a schedule.

Resumes the recurrence. The next cycle runs at the next scheduled time after
now. A run the pause spanned is not backfilled, but it is not used up either:
a Recurrences or Custom Date schedule still makes every run it counted at
creation, which carries a Custom Date schedule past its end date by about as
long as it was paused.

Activating a Fulfilled schedule, one whose ending was met, sets it Active, but
it does not run again; stderr says so when the schedule returned shows every
counted run made. Create a new schedule to keep refreshing.`)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := parseID(args[0], "schedule")
		if err != nil {
			return err
		}
		c, err := client()
		if err != nil {
			return err
		}

		// Raw, so --json prints the server's envelope as postID does.
		raw, err := c.PostRaw(cmd.Context(), schedulesPrefix+"/activate", map[string]int{"id": id})
		if err != nil {
			if jsonOutput {
				printErrorEnvelope(cmd, err)
			}
			return err
		}
		if jsonOutput {
			if err := output.JSON(cmd.OutOrStdout(), raw); err != nil {
				return err
			}
		} else {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "activated schedule %d\n", id)
		}
		if spentSchedule(raw) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "note: schedule %d has already made every run its "+
				"ending allows, so it reads Active but will not run again; create a new schedule "+
				"to keep refreshing\n", id)
		}
		return nil
	}
	return cmd
}

// spentSchedule reports whether the schedule in an activate response has made
// every run its ending counted, the API's own test for arming no next run:
// cycles.available not above cycles.done, on any ending but 1 Never. Anything
// missing or unreadable reads as not spent, so this can only add a note.
func spentSchedule(envelope []byte) bool {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(envelope, &env) != nil {
		return false
	}
	data := bytes.TrimSpace(env.Data)
	if len(data) > 0 && data[0] == '[' {
		var list []json.RawMessage
		if json.Unmarshal(data, &list) != nil || len(list) == 0 {
			return false
		}
		data = list[0]
	}
	var rec struct {
		Recurrence *struct {
			Ending *struct {
				Type float64 `json:"type"`
			} `json:"ending"`
			Cycles *struct {
				Done      *float64 `json:"done"`
				Available *float64 `json:"available"`
			} `json:"cycles"`
		} `json:"recurrence"`
	}
	if json.Unmarshal(data, &rec) != nil {
		return false
	}
	r := rec.Recurrence
	if r == nil || r.Ending == nil || r.Cycles == nil || r.Cycles.Done == nil || r.Cycles.Available == nil {
		return false
	}
	if r.Ending.Type == 1 {
		return false // Never: there is no count to use up
	}
	return *r.Cycles.Available <= *r.Cycles.Done
}

// cases upper-cases the first letter, for a Short that must start capitalised.
func cases(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func schedulesListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List schedules",
		Args:  cobra.NoArgs,
	}
	query := listFlags(cmd, "Case-insensitive contains match on the schedule name")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return renderList(cmd, schedulesPrefix+"/index", query(), scheduleColumns, "no schedules")
	}
	return cmd
}

func init() {
	schedulesCmd.AddCommand(
		schedulesListCommand(),
		showCommand("schedule", schedulesPrefix, scheduleDetail),
		deleteCommand("schedule", schedulesPrefix),

		schedulesCreateCommand(),
		schedulesActivateCommand(),
		toggleCommand("deactivate", "deactivate", "deactivated",
			`Deactivate a schedule.

Pauses the recurrence without deleting it. Audiences already built by earlier
cycles are untouched.`),
	)
	rootCmd.AddCommand(schedulesCmd)
}
