package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/intuizi/intuizi-cli/internal/api"
	"github.com/intuizi/intuizi-cli/internal/output"
)

// Shared runners for the resource commands.
//
// These are helpers each resource's own file calls, not a framework it enrols
// in. What they hold is real cross-cutting policy - data on stdout and
// commentary on stderr, --json passing the envelope through untouched, an
// unset flag never being sent - which must not drift between resources.
//
// The commands themselves are declared per resource, so a divergence costs one
// line at the call site rather than another field on a shared struct.

// showCommand is GET {prefix}/{id}, rendered as key/value lines.
func showCommand(singular, prefix string, cols []string) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one " + singular,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], singular)
			if err != nil {
				return err
			}
			return renderOne(cmd, prefix+"/"+strconv.Itoa(id), cols)
		},
	}
}

// deleteCommand is POST {prefix}/delete-by-id with {"id": n}.
func deleteCommand(singular, prefix string) *cobra.Command {
	var yes bool

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete one " + singular,
		Long: fmt.Sprintf(`Delete one %s.

Asks for confirmation unless --yes is given. Deleting is not reversible.`, singular),
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0], singular)
			if err != nil {
				return err
			}
			if !yes {
				q := fmt.Sprintf("Delete %s %d?", singular, id)
				if err := confirm(cmd, q); err != nil {
					return err
				}
			}
			return postID(cmd, prefix+"/delete-by-id", id,
				fmt.Sprintf("deleted %s %d", singular, id))
		},
	}

	cmd.Flags().BoolVar(&yes, "yes", false, "Skip the confirmation prompt")
	return cmd
}

// --------------------------------------------------------------------------------- shared runners

// renderList performs a paginated read: data on stdout, commentary on stderr,
// so a pipe only ever sees the payload.
func renderList(cmd *cobra.Command, path string, query url.Values, cols []string, empty string) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.GetRaw(cmd.Context(), path, query)
		if err != nil {
			// The envelope still goes out on a failure: --json promises what
			// the server sent, and a script reads 422 field errors from it.
			var apiErr *api.Error
			if errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
				_ = output.JSON(cmd.OutOrStdout(), apiErr.Body)
			}
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	items, pg, err := api.ReadList[output.Record](cmd.Context(), c, path, query)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), empty)
		return nil
	}

	if quietOutput {
		if err := output.IDs(cmd.OutOrStdout(), items); err != nil {
			return err
		}
	} else if len(cols) > 0 {
		rows := make([]output.Record, len(items))
		for i, item := range items {
			rows[i] = summarise(item, cols)
		}
		if err := output.TableWith(cmd.OutOrStdout(), rows, cols); err != nil {
			return err
		}
	} else if err := output.Table(cmd.OutOrStdout(), items); err != nil {
		return err
	}

	output.Footer(cmd.ErrOrStderr(), pg)
	return nil
}

// renderOne performs a single-resource read, printed as key/value lines. lead
// orders the fields worth seeing first; the rest follow alphabetically.
func renderOne(cmd *cobra.Command, path string, lead []string) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.GetRaw(cmd.Context(), path, nil)
		if err != nil {
			// As in renderList: the error envelope is still data for --json.
			var apiErr *api.Error
			if errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
				_ = output.JSON(cmd.OutOrStdout(), apiErr.Body)
			}
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	item, err := api.Read[output.Record](cmd.Context(), c, path, nil)
	if err != nil {
		return err
	}
	if quietOutput {
		return output.IDs(cmd.OutOrStdout(), []output.Record{item})
	}
	return output.Detail(cmd.OutOrStdout(), flatten(item), lead)
}

// postID posts {"id": n} and reports success on stderr, keeping stdout empty
// for a pipe. With --json the envelope is printed instead.
func postID(cmd *cobra.Command, path string, id int, done string) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.PostRaw(cmd.Context(), path, map[string]int{"id": id})
		if err != nil {
			// As in renderList: the error envelope is still data for --json.
			var apiErr *api.Error
			if errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
				_ = output.JSON(cmd.OutOrStdout(), apiErr.Body)
			}
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	if err := c.Post(cmd.Context(), path, map[string]int{"id": id}, nil); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), done)
	return nil
}

// createFromFile posts a caller-supplied JSON body to path and prints the
// created resource. Used by every create whose payload is too nested to model
// as flags.
func createFromFile(cmd *cobra.Command, path, file string, lead []string, next string) error {
	payload, err := readPayload(cmd, file)
	if err != nil {
		return err
	}
	return createBody(cmd, path, payload, lead, next)
}

// createBody posts an already-built body, for the creates whose whole payload
// fits in a flag or two.
func createBody(cmd *cobra.Command, path string, payload any, lead []string, next string) error {
	c, err := client()
	if err != nil {
		return err
	}

	if jsonOutput {
		raw, err := c.PostRaw(cmd.Context(), path, payload)
		if err != nil {
			// As in renderList: the error envelope is still data for --json.
			var apiErr *api.Error
			if errors.As(err, &apiErr) && len(apiErr.Body) > 0 {
				_ = output.JSON(cmd.OutOrStdout(), apiErr.Body)
			}
			return err
		}
		return output.JSON(cmd.OutOrStdout(), raw)
	}

	created, err := api.Create[output.Record](cmd.Context(), c, path, payload)
	if err != nil {
		return err
	}
	if quietOutput {
		return output.IDs(cmd.OutOrStdout(), []output.Record{created})
	}
	if err := output.Detail(cmd.OutOrStdout(), flatten(created), lead); err != nil {
		return err
	}
	if next != "" {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), next)
	}
	return nil
}

// --------------------------------------------------------------------------------- helpers

// readPayload takes the create body from a file, or from stdin when file is
// "-", so 'jq ... | intuizi audiences create --file -' works in a script.
//
// The bytes are passed through untouched rather than re-marshalled, so a field
// this CLI has never heard of still reaches the API. It is only checked to be a
// JSON object: an offline error beats spending a round trip on a 422.
func readPayload(cmd *cobra.Command, file string) (json.RawMessage, error) {
	var (
		raw []byte
		err error
	)

	if file == "-" {
		raw, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return nil, fmt.Errorf("reading the payload from stdin: %w", err)
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return nil, errors.New("no payload on stdin")
		}
	} else {
		raw, err = os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", file, err)
		}
	}

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("%s does not hold a JSON object", payloadName(file))
	}
	if !json.Valid(trimmed) {
		return nil, fmt.Errorf("invalid JSON in %s", payloadName(file))
	}
	return json.RawMessage(trimmed), nil
}

func payloadName(file string) string {
	if file == "-" {
		return "the payload on stdin"
	}
	return file
}

// confirm asks a yes/no question on stderr. A non-terminal stdin fails rather
// than blocking: a prompt nobody can answer is a hung script.
func confirm(cmd *cobra.Command, question string) error {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && !term.IsTerminal(int(f.Fd())) {
		return usageErr("not a terminal - pass --yes to confirm")
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "%s [y/N] ", question)

	// The read runs aside so a Ctrl-C at the prompt is seen: a blocking read
	// on stdin cannot be interrupted, and only the context's error lets
	// Execute map the signal to its exit code. The reader is left behind when
	// cancelled; the process is exiting.
	type answer struct {
		line string
		err  error
	}
	ch := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		ch <- answer{line, err}
	}()

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background() // built outside cobra's Execute, as in a test
	}
	var a answer
	select {
	case <-ctx.Done():
		return ctx.Err()
	case a = <-ch:
	}
	if a.err != nil && a.line == "" {
		return errors.New("aborted")
	}
	switch strings.ToLower(strings.TrimSpace(a.line)) {
	case "y", "yes":
		return nil
	default:
		return errors.New("aborted")
	}
}

// parseID rejects a bad id locally rather than spending a round trip on it.
// A usage error: the command line is wrong, and nothing was sent.
func parseID(arg, singular string) (int, error) {
	id, err := strconv.Atoi(strings.TrimSpace(arg))
	if err != nil || id <= 0 {
		return 0, usageErr(fmt.Sprintf("%q is not a valid %s id", arg, singular))
	}
	return id, nil
}

// summarise trims one index row to the display columns. A nested {id, name}
// object - status, project, audience, partner - would render as {...}, so it
// collapses to its name here rather than in the output package, which stays
// endpoint-agnostic. A column the response omits renders as an empty cell.
func summarise(item output.Record, cols []string) output.Record {
	row := make(output.Record, len(cols))
	for _, col := range cols {
		row[col] = name(item[col])
	}
	return row
}

// flatten applies the same {id, name} collapse across a whole record, for the
// key/value detail view.
func flatten(item output.Record) output.Record {
	out := make(output.Record, len(item))
	for k, v := range item {
		out[k] = name(v)
	}
	return out
}

// name unwraps {"id": .., "name": ".."} to the name and joins a list of
// scalars, leaving anything else be: a list holding maps or lists is better
// counted by the cell than dumped into it.
func name(v any) any {
	switch t := v.(type) {
	case map[string]any:
		if n, ok := t["name"]; ok {
			return n
		}
	case []any:
		if s, ok := joinScalars(t); ok {
			return s
		}
	}
	return v
}

// joinScalars renders ["a", "b"] as "a, b", so webhooks list shows its events.
// An empty list is left alone: "[0 items]" says there are none, a blank cell
// does not.
func joinScalars(list []any) (string, bool) {
	if len(list) == 0 {
		return "", false
	}
	parts := make([]string, len(list))
	for i, e := range list {
		switch s := e.(type) {
		case string:
			parts[i] = s
		case json.Number:
			parts[i] = s.String()
		case bool:
			parts[i] = strconv.FormatBool(s)
		default:
			return "", false
		}
	}
	return strings.Join(parts, ", "), true
}

// createRecord posts a body and returns the created record without printing
// it, for a caller that needs the new id before deciding what to show - a
// --wait create prints the final state, not the Initiating one.
func createRecord(cmd *cobra.Command, c *api.Client, path string, payload any) (output.Record, error) {
	return api.Create[output.Record](cmd.Context(), c, path, payload)
}

// idOf reads the integer id off a record. Numbers are json.Number (UseNumber),
// so a float64 assertion would silently yield 0.
func idOf(rec output.Record) (int, error) {
	n, ok := rec["id"].(json.Number)
	if !ok {
		return 0, errors.New("response carried no id")
	}
	id, err := strconv.Atoi(n.String())
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("response carried an invalid id %q", n.String())
	}
	return id, nil
}
