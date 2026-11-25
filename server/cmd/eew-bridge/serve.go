package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"quakerelay/server/internal/eewbridge"
)

type bridgeConfig struct {
	Database, Listen, HandoffSocket string
}

func loadConfig() bridgeConfig {
	get := func(key, fallback string) string {
		if v := os.Getenv(key); v != "" {
			return v
		}
		return fallback
	}
	return bridgeConfig{
		Database:      get("EEW_BRIDGE_DATABASE", "/var/lib/quick-relay-bridge/bridge.db"),
		Listen:        get("EEW_BRIDGE_LISTEN", "127.0.0.1:9082"),
		HandoffSocket: get("EEW_HANDOFF_SOCKET", "/run/quick-relay-handoff/handoff.sock"),
	}
}

func (c bridgeConfig) validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if ip := net.ParseIP(host); err != nil || ip == nil || !ip.IsLoopback() {
		return errors.New("EEW_BRIDGE_LISTEN must be a loopback IP and port; use Caddy for public TLS")
	}
	if c.Database == "" || c.HandoffSocket == "" {
		return errors.New("EEW_BRIDGE_DATABASE and EEW_HANDOFF_SOCKET are required")
	}
	return nil
}

func serve(c bridgeConfig) error {
	if err := c.validate(); err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	st, err := eewbridge.Open(c.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	ln, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	health := &eewbridge.Health{}
	server := &eewbridge.Server{Store: st, Health: health, Logger: logger}
	feeder := &eewbridge.Feeder{Store: st, Health: health, Logger: logger, Socket: c.HandoffSocket}
	var wg sync.WaitGroup
	for _, task := range []func(context.Context){health.Run, feeder.Run, server.WatchRevocations, func(ctx context.Context) { prune(ctx, st, logger) }} {
		wg.Add(1)
		go func() { defer wg.Done(); task(ctx) }()
	}
	logger.Info("EEW bridge started", "listen", c.Listen, "handoff_socket", c.HandoffSocket)
	err = server.Serve(ctx, ln)
	stop()
	wg.Wait()
	logger.Info("EEW bridge stopped")
	return err
}

func prune(ctx context.Context, st *eewbridge.Store, logger *slog.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		if removed, err := st.Prune(ctx, time.Now(), eewbridge.DefaultRetention); err != nil && ctx.Err() == nil {
			logger.Warn("EEW bridge pruning failed", "error", err.Error())
		} else if removed > 0 {
			logger.Info("EEW delivery log pruned", "entries", removed)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func tokenCommand(c bridgeConfig, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	st, err := eewbridge.Open(c.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	switch args[0] {
	case "issue":
		flags := flag.NewFlagSet("token issue", flag.ContinueOnError)
		name := flags.String("name", "", "device label, at most 64 characters")
		if err = flags.Parse(args[1:]); err != nil {
			return err
		}
		if flags.NArg() != 0 || strings.TrimSpace(*name) == "" {
			return errors.New("usage: eew-bridge token issue -name NAME")
		}
		t, secret, err := st.IssueToken(ctx, *name, time.Now())
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Token %s for %q (environment %s). Shown once; store it in the device's credential store:\n%s\n", t.ID, t.Name, t.Environment, secret)
		return nil
	case "list":
		if len(args) != 1 {
			return errors.New(usage)
		}
		tokens, err := st.Tokens(ctx)
		if err != nil {
			return err
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tENVIRONMENT\tCREATED\tREVOKED")
		for _, t := range tokens {
			revoked := "-"
			if t.RevokedAt != nil {
				revoked = t.RevokedAt.Format(time.RFC3339)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Name, t.Environment, t.CreatedAt.Format(time.RFC3339), revoked)
		}
		return w.Flush()
	case "revoke":
		if len(args) != 2 {
			return errors.New("usage: eew-bridge token revoke ID")
		}
		if err = st.RevokeToken(ctx, args[1], time.Now()); err != nil {
			return err
		}
		fmt.Fprintln(stdout, "Token revoked; its sessions close within a few seconds.")
		return nil
	}
	return errors.New(usage)
}

// backup writes a consistent snapshot of the bridge database. A bridge that has
// never started has nothing to save, and no database is created for it.
func backup(c bridgeConfig, target string, stdout io.Writer) error {
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		return errors.New("backup target must not already exist")
	}
	if _, err := os.Stat(c.Database); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stdout, "No bridge database yet; nothing to back up.")
		return nil
	}
	st, err := eewbridge.Open(c.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err = st.DB.ExecContext(context.Background(), "VACUUM INTO ?", target); err != nil {
		return err
	}
	if err = os.Chmod(target, 0600); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "Bridge database snapshot created. After restoring one, run rotate-epoch before serve.")
	return nil
}

// rotateEpoch starts a new delivery epoch, for example after restoring bridge.db
// from a backup, so clients replace their state instead of resuming.
func rotateEpoch(c bridgeConfig, stdout io.Writer) error {
	st, err := eewbridge.Open(c.Database)
	if err != nil {
		return err
	}
	defer st.Close()
	if _, err = st.Reconcile(context.Background(), time.Now(), true); err != nil {
		return err
	}
	stream, err := st.Stream(context.Background(), eewbridge.StreamProduction)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "New delivery epoch %s; active warnings are marked partial.\n", stream.Epoch)
	return nil
}
