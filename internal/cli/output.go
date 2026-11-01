package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// ---------------------------------------------------------------------------
// output helpers (respect --json and --quiet, colorize on a TTY)
// ---------------------------------------------------------------------------

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

const (
	ansiReset  = "\x1b[0m"
	ansiRed    = "\x1b[31m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiBold   = "\x1b[1m"
	ansiDim    = "\x1b[2m"
)

func paint(code, s string) string {
	if !isTTY(os.Stdout) || flagQuiet {
		return s
	}
	return code + s + ansiReset
}

// printJSON emits machine-readable output.
func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// table prints aligned columns.
func table(headers []string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, paint(ansiBold, strings.Join(headers, "\t")))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
}

func success(format string, args ...any) {
	if flagQuiet || flagJSON {
		return
	}
	fmt.Fprintln(os.Stdout, paint(ansiGreen, "✔ ")+fmt.Sprintf(format, args...))
}

func info(format string, args ...any) {
	if flagQuiet || flagJSON {
		return
	}
	fmt.Fprintln(os.Stdout, fmt.Sprintf(format, args...))
}

func warn(format string, args ...any) {
	if flagJSON {
		return
	}
	fmt.Fprintln(os.Stderr, paint(ansiYellow, "⚠ ")+fmt.Sprintf(format, args...))
}

// errorf prints a failure to stderr.
func errorf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, paint(ansiRed, "✘ ")+fmt.Sprintf(format, args...))
}

// confirmYes reads a y/N answer from stdin (used for destructive confirms).
func confirmYes(prompt string) bool {
	if !isTTY(os.Stdin) {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	var answer string
	_, _ = fmt.Fscanln(os.Stdin, &answer)
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes"
}
