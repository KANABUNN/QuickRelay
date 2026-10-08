package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"quakerelay/server/internal/api"
	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/config"
	"quakerelay/server/internal/dmdata"
	"quakerelay/server/internal/relay"
	"quakerelay/server/internal/store"
)

func run(args []string) error {
	flags := flag.NewFlagSet("quakerelay", flag.ContinueOnError)
	envFile := flags.String("env-file", "", "literal KEY=value file (environment takes precedence)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if err := config.LoadEnv(*envFile); err != nil {
		return err
	}
	c := config.Load()
	args = flags.Args()
	if len(args) == 0 {
		return errors.New("usage: quakerelay [-env-file FILE] serve|check|pair|revoke ID|replay FILE|backup FILE")
	}
	if args[0] == "check" {
		if err := c.Validate(); err != nil {
			return err
		}
		if c.Mode == "live" {
			if _, err := c.NewAPNsProvider(); err != nil {
				return err
			}
		}
		fmt.Printf("configuration valid; mode=%s (live connectivity not checked)\n", c.Mode)
		return nil
	}
	if args[0] == "serve" {
		if err := c.Validate(); err != nil {
			return err
		}
	}
	st, err := store.Open(c.DBPath)
	if err != nil {
		return err
	}
	defer st.Close()
	ctx := context.Background()
	switch args[0] {
	case "pair":
		code, err := st.CreatePairing(ctx, time.Now())
		if err != nil {
			return err
		}
		fmt.Printf("Pairing code: %s (expires in 10 minutes; replaces any unused code)\n", code)
		return nil
	case "revoke":
		if len(args) != 2 || !store.ValidID(args[1]) {
			return errors.New("usage: quakerelay revoke INSTALLATION_ID")
		}
		if err := st.Revoke(ctx, args[1]); err != nil {
			return err
		}
		fmt.Println("Device revoked.")
		return nil
	case "replay":
		if len(args) != 2 {
			return errors.New("usage: quakerelay replay JSONL_FILE")
		}
		return replay(ctx, st, args[1])
	case "backup":
		if len(args) != 2 {
			return errors.New("usage: quakerelay backup NEW_FILE")
		}
		if _, err := os.Stat(args[1]); !errors.Is(err, os.ErrNotExist) {
			return errors.New("backup target must not already exist")
		}
		if _, err := st.DB.ExecContext(ctx, "VACUUM INTO ?", args[1]); err != nil {
			return err
		}
		if err := os.Chmod(args[1], 0600); err != nil {
			return err
		}
		fmt.Println("SQLite snapshot created.")
		return nil
	case "serve":
		return serve(c, st)
	default:
		return errors.New("unknown command")
	}
}
func replay(ctx context.Context, st *store.Store, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 65536), dmdata.MaxMessageBytes)
	n := 0
	for scanner.Scan() {
		r, err := dmdata.Normalize(scanner.Bytes(), time.Now())
		if errors.Is(err, dmdata.ErrIgnored) {
			continue
		}
		if err != nil {
			return fmt.Errorf("replay line %d: %w", n+1, err)
		}
		if _, err = st.IngestHistorical(ctx, r); err != nil {
			return err
		}
		n++
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	fmt.Printf("Imported %d operational telegrams as history; no deliveries enqueued.\n", n)
	return nil
}
func serve(c config.Config, st *store.Store) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app := api.New(st, c.PairingSecret)
	var source *dmdata.Client
	var sender *apns.Provider
	if c.Mode == "live" {
		var err error
		sender, err = c.NewAPNsProvider()
		if err != nil {
			return err
		}
		source, err = dmdata.NewWithClassifications(c.DMDATAToken, c.DMDATAAuthMode, c.DMDATAClassifications)
		if err != nil {
			return err
		}
		app.TestSender = sender
		app.APNsConfigured = true
		app.APNsEnvironmentAllowed = sender.Supports
		app.Source = source
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: app, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	httpErr := make(chan error, 1)
	go func() { httpErr <- server.Serve(listener) }()
	var wg sync.WaitGroup
	fatal := make(chan error, 1)
	if source != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			source.Run(ctx, func(ctx context.Context, frame []byte) error {
				r, err := dmdata.Normalize(frame, time.Now())
				if errors.Is(err, dmdata.ErrIgnored) {
					return nil
				}
				if err != nil {
					source.Rejected()
					logger.Warn("DMDATA telegram rejected", "reason", err.Error())
					return nil
				}
				result, err := st.Ingest(ctx, r)
				if err != nil {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					select {
					case fatal <- errors.New("ingestion persistence failed"):
					default:
					}
					stop()
					return errors.New("ingestion persistence failed")
				}
				if result.Inserted {
					logger.Info("telegram committed", "sequence", result.Sequence, "current", result.Current, "kind", r.Classification)
				}
				return nil
			})
		}()
		// Claims serialize each device, and bound total concurrent HTTP/2 requests.
		for range 4 {
			wg.Add(1)
			go func() { defer wg.Done(); (&relay.Worker{Store: st, Sender: sender, Logger: logger}).Run(ctx) }()
		}
		wg.Add(1)
		go func() { defer wg.Done(); (&relay.Worker{Store: st, Sender: sender, Logger: logger}).RunLive(ctx) }()
	}
	logger.Info("QuakeRelay started", "mode", c.Mode, "listen", c.Listen)
	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-httpErr:
		if !errors.Is(err, http.ErrServerClosed) {
			serveErr = err
		}
		stop()
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		server.Close()
	}
	stop()
	wg.Wait()
	select {
	case err := <-fatal:
		return err
	default:
	}
	logger.Info("QuakeRelay stopped")
	return serveErr
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
