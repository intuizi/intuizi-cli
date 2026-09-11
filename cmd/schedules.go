package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	// The static binary must resolve --timezone on hosts with no tz database.
	_ "time/tzdata"

	"github.com/spf13/cobra"
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
      --frequency weekly --window 2

--frequency, --window and --ending take values from the catalogs:

    intuizi reference common schedule-frequencies
    intuizi reference common schedule-windows
    intuizi reference common schedule-endings

--start must be in the future and is read in --timezone. --window 3 is Custom
and also needs --window-days. --ending defaults to 1 Never; 2 Recurrences needs
--after-recurrences and 3 Custom Date needs --end-date.

An activation block for auto-export is nested, so a schedule that exports every
cycle is passed whole instead:

    intuizi schedules create --file schedule.json

A retry of this command reuses its Idempotency-Key, so it cannot create a
duplicate schedule.`,
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
		startAt, err := time.ParseInLocation(startLayout, start, loc)
		if err != nil {
			return usageErr(fmt.Sprintf(`--start must be "YYYY-MM-DD HH:MM:SS", not %q`, start))
		}
		if !startAt.After(time.Now()) {
			return usageErr("--start " + start + " must be in the future in " + timezone)
		}

		// Each rule carries its own field, and sending the wrong one is not
		// rejected - it is ignored, leaving a schedule that never stops. An
		// explicit 0 is a mistake to name, so these go by Changed, not value.
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
			if _, err := time.Parse(dateLayout, endDate); err != nil {
				return usageErr("--end-date must be YYYY-MM-DD, not " + endDate)
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
	f.StringVar(&timezone, "timezone", "", "IANA timezone, e.g. America/New_York")
	f.StringVar(&frequency, "frequency", "",
		"daily, weekly, bi-weekly or monthly (case-insensitive)")
	f.IntVar(&window, "window", 0,
		"Data window id from 'reference common schedule-windows'")
	f.IntVar(&windowDays, "window-days", 0, "Day count for --window 3 Custom")
	f.IntVar(&ending, "ending", 1,
		"Stop rule from 'reference common schedule-endings': 1 Never, 2 Recurrences, 3 Custom Date")
	f.IntVar(&after, "after-recurrences", 0, "Number of runs before stopping (--ending 2)")
	f.StringVar(&endDate, "end-date", "", "Last run date, YYYY-MM-DD (--ending 3)")
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
		toggleCommand("activate", "activate", "activated",
			`Activate a schedule.

Resumes the recurrence. The next cycle runs at the next scheduled time; a
missed window while it was paused is not backfilled.`),
		toggleCommand("deactivate", "deactivate", "deactivated",
			`Deactivate a schedule.

Pauses the recurrence without deleting it. Audiences already built by earlier
cycles are untouched.`),
	)
	rootCmd.AddCommand(schedulesCmd)
}
