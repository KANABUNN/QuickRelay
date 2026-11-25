// Command eew-bridge maintains EEW state for the voice clients, separately from
// the relay, and delivers it over WebSocket (docs/eew-bridge.md).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"quakerelay/server/internal/config"
	"quakerelay/server/internal/eewbridge"
)

const usage = `usage:
  eew-bridge [-env-file FILE] serve
  eew-bridge [-env-file FILE] token issue -name NAME | token list | token revoke ID
  eew-bridge [-env-file FILE] rotate-epoch
  eew-bridge verify -source-db RELAY_COPY.db [-state-db NEW_BRIDGE.db] [-json]`

// errMismatch makes verification differences visible in the exit status.
var errMismatch = errors.New("bridge and relay disagree")

func run(args []string, stdout io.Writer) error {
	global := flag.NewFlagSet("eew-bridge", flag.ContinueOnError)
	envFile := global.String("env-file", "", "literal KEY=value file (environment takes precedence)")
	if err := global.Parse(args); err != nil {
		return err
	}
	args = global.Args()
	if len(args) == 0 {
		return errors.New(usage)
	}
	if args[0] == "verify" {
		return verifyCommand(args[1:], stdout)
	}
	if err := config.LoadEnv(*envFile); err != nil {
		return err
	}
	c := loadConfig()
	switch args[0] {
	case "serve":
		if len(args) != 1 {
			return errors.New(usage)
		}
		return serve(c)
	case "token":
		return tokenCommand(c, args[1:], stdout)
	case "rotate-epoch":
		return rotateEpoch(c, stdout)
	}
	return errors.New(usage)
}

func verifyCommand(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("verify", flag.ContinueOnError)
	source := flags.String("source-db", "", "copy of the relay database (opened read-only)")
	statePath := flags.String("state-db", "", "new bridge database to keep; a temporary one is used otherwise")
	asJSON := flags.Bool("json", false, "print the report as JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *source == "" || flags.NArg() != 0 {
		return errors.New(usage)
	}
	path := *statePath
	if path == "" {
		dir, err := os.MkdirTemp("", "eew-bridge-verify-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		path = filepath.Join(dir, "bridge.db")
	} else if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return errors.New("state database must not already exist")
	}
	relay, err := eewbridge.OpenRelayCopy(*source)
	if err != nil {
		return err
	}
	defer relay.Close()
	bridge, err := eewbridge.Open(path)
	if err != nil {
		return err
	}
	defer bridge.Close()
	report, err := eewbridge.Verify(context.Background(), relay, bridge)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err = enc.Encode(report); err != nil {
			return err
		}
	} else {
		printReport(stdout, report)
	}
	if !report.Clean() {
		return errMismatch
	}
	return nil
}

func printReport(w io.Writer, r eewbridge.VerifyReport) {
	fmt.Fprintf(w, "EEW reports: %d, series: %d, partial warnings: %d\n", r.Reports, r.Series, r.PartialWarnings)
	labels := make([]string, 0, len(r.Outcomes))
	for label := range r.Outcomes {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		fmt.Fprintf(w, "  %-40s %d\n", label, r.Outcomes[label])
	}
	fmt.Fprintf(w, "Series mismatches: %d\n", len(r.Mismatches))
	for _, m := range r.Mismatches {
		fmt.Fprintf(w, "  %s %s relay=%s bridge=%s\n", m.EventID, m.Series, m.Relay, m.Bridge)
	}
	fmt.Fprintf(w, "Applied disagreements: %d\n", len(r.Disagreements))
	for _, d := range r.Disagreements {
		fmt.Fprintf(w, "  report %d %s relay_current=%v bridge=%s %s\n", d.ReportSequence, d.EventID, d.RelayCurrent, d.Outcome, d.Reason)
	}
	fmt.Fprintf(w, "Stored relay streams decided differently today: %d\n", len(r.StoredDrift))
	for _, m := range r.StoredDrift {
		fmt.Fprintf(w, "  %s %s stored=%s now=%s\n", m.EventID, m.Series, m.Relay, m.Bridge)
	}
	fmt.Fprintf(w, "Quarantined: %d\n", len(r.Quarantined))
	for _, q := range r.Quarantined {
		fmt.Fprintf(w, "  %s %s %s %s\n", q.Code, q.EventID, q.TelegramID, q.Reason)
	}
}

func main() {
	err := run(os.Args[1:], os.Stdout)
	switch {
	case errors.Is(err, errMismatch):
		fmt.Fprintln(os.Stderr, err)
		os.Exit(3)
	case err != nil:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
