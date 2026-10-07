// apns-send sends one explicitly labelled test notification without DMDATA.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/config"
	"quakerelay/server/internal/store"
)

func run() error {
	envFile := flag.String("env-file", "", "literal KEY=value file")
	environment := flag.String("environment", "", "sandbox/development or production for APNS_DEVICE_TOKEN")
	installation := flag.String("installation-id", "", "send to an already registered installation without exposing its token")
	flag.Parse()
	if err := config.LoadEnv(*envFile); err != nil {
		return err
	}
	cfg := config.Load()
	provider, err := cfg.NewAPNsProvider()
	if err != nil {
		return err
	}
	token, env := os.Getenv("APNS_DEVICE_TOKEN"), cfg.APNsEnvironment
	if *environment != "" {
		env = *environment
	}
	if *installation != "" {
		if !store.ValidID(*installation) {
			return errors.New("invalid installation ID")
		}
		// A typo in DATABASE_PATH must not silently create an empty database.
		if _, err := os.Stat(cfg.DBPath); err != nil {
			return errors.New("device database is unavailable")
		}
		db, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer db.Close()
		device, err := db.Device(context.Background(), *installation)
		if err != nil {
			return err
		}
		if !device.Active || !device.PushActive {
			return errors.New("device is not an active Push recipient")
		}
		token, env = device.Token, device.Environment
	}
	if env != "development" && env != "sandbox" && env != "production" {
		return errors.New("select one -environment or use -installation-id")
	}
	if !provider.Supports(env) {
		return errors.New("selected APNs environment is not configured")
	}
	payload, _ := json.Marshal(map[string]any{"aps": map[string]any{"alert": map[string]string{"title": "Quick Relay 接続テスト", "body": "これは通知経路のテストです。地震情報ではありません。"}, "sound": "default"}, "kind": "system_test"})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := provider.Send(ctx, apns.Request{Token: token, Environment: env, Payload: payload, Priority: 10})
	if err != nil {
		return err
	}
	if !result.Accepted() {
		return fmt.Errorf("APNs rejected: status=%d reason=%s", result.Status, result.Reason)
	}
	fmt.Printf("APNs accepted test: id=%s (device delivery is not confirmed)\n", result.ID)
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
