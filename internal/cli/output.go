package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// printList writes rows as a table, or items as JSON with --json.
func printList(w io.Writer, items any, header []string, rows [][]string) error {
	if asJSON {
		return printJSON(w, items)
	}
	t := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(t, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(t, strings.Join(row, "\t"))
	}
	return t.Flush()
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// say prints progress and confirmations, which -q silences. Results a command
// was asked for, and errors, are never silenced.
func say(w io.Writer, format string, args ...any) {
	if !quiet {
		fmt.Fprintf(w, format+"\n", args...)
	}
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
