package config

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"

	"quakerelay/server/internal/apns"
	"quakerelay/server/internal/dmdata"
)

type Config struct {
	DMDATAClassifications                        []string
	Mode, Listen, DBPath, PairingSecret          string
	DMDATAToken, DMDATAAuthMode                  string
	APNsTeam, APNsKeyID, APNsBundle, APNsKeyFile string
	APNsEnvironment                              string
	APNsSandboxKeyID, APNsSandboxKeyFile         string
	APNsProductionKeyID, APNsProductionKeyFile   string
}

// LoadEnv reads literal KEY=value entries. It never invokes a shell or expands variables.
// Existing process environment takes precedence over the optional file.
func LoadEnv(path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	line := 0
	for scan.Scan() {
		line++
		s := strings.TrimSpace(scan.Text())
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		k, v, ok := strings.Cut(s, "=")
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if !ok || k == "" || strings.Trim(k, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789") != "" {
			return fmt.Errorf("invalid environment file line %d", line)
		}
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		if _, exists := os.LookupEnv(k); !exists {
			if err = os.Setenv(k, v); err != nil {
				return err
			}
		}
	}
	return scan.Err()
}
func Load() Config {
	get := func(k, fallback string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return fallback
	}
	return Config{
		DMDATAClassifications: strings.Split(get("DMDATA_CLASSIFICATIONS", "eew.forecast,eew.warning,telegram.earthquake"), ","),
		Mode:                  get("RELAY_MODE", "live"), Listen: get("LISTEN_ADDR", "127.0.0.1:8080"), DBPath: get("DATABASE_PATH", "quakerelay.db"),
		PairingSecret: os.Getenv("PAIRING_SECRET"), DMDATAToken: get("DMDATA_API_KEY", os.Getenv("DMDATA_TOKEN")), DMDATAAuthMode: get("DMDATA_AUTH_MODE", "api_key"),
		APNsTeam: os.Getenv("APNS_TEAM_ID"), APNsKeyID: os.Getenv("APNS_KEY_ID"), APNsBundle: os.Getenv("APNS_BUNDLE_ID"), APNsKeyFile: get("APNS_KEY_PATH", os.Getenv("APNS_KEY_FILE")),
		APNsEnvironment:  get("APNS_ENVIRONMENT", "development"),
		APNsSandboxKeyID: os.Getenv("APNS_SANDBOX_KEY_ID"), APNsSandboxKeyFile: os.Getenv("APNS_SANDBOX_KEY_PATH"),
		APNsProductionKeyID: os.Getenv("APNS_PRODUCTION_KEY_ID"), APNsProductionKeyFile: os.Getenv("APNS_PRODUCTION_KEY_PATH"),
	}
}
func (c Config) Validate() error {
	if c.Mode != "live" && c.Mode != "offline" {
		return errors.New("RELAY_MODE must be live or offline")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("LISTEN_ADDR must be a loopback IP and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("LISTEN_ADDR must bind loopback; use Caddy for public HTTPS")
	}
	if len(c.PairingSecret) < 32 || strings.Contains(c.PairingSecret, "REPLACE_") {
		return errors.New("set PAIRING_SECRET to at least 32 random characters")
	}
	if c.Mode == "live" {
		required := map[string]string{"DMDATA_TOKEN": c.DMDATAToken, "APNS_TEAM_ID": c.APNsTeam, "APNS_BUNDLE_ID": c.APNsBundle}
		for key, value := range required {
			if value == "" || strings.Contains(value, "REPLACE_") || strings.Contains(value, "example") {
				return fmt.Errorf("set %s before starting live mode", key)
			}
		}
		if _, err := c.APNsCredentials(); err != nil {
			return err
		}
		if _, err := dmdata.NewWithClassifications(c.DMDATAToken, c.DMDATAAuthMode, c.DMDATAClassifications); err != nil {
			return err
		}
	}
	return nil
}

// APNsCredentials never applies a key intended for one environment to another.
// "both" must be explicitly requested for legacy shared keys or two scoped keys.
func (c Config) APNsCredentials() (map[string]apns.Credentials, error) {
	env := c.APNsEnvironment
	if env == "" || env == "sandbox" {
		env = "development"
	}
	if env != "development" && env != "production" && env != "both" {
		return nil, errors.New("APNS_ENVIRONMENT must be sandbox, development, production or both")
	}
	result := make(map[string]apns.Credentials)
	for _, selected := range []string{"development", "production"} {
		if env != "both" && selected != env {
			continue
		}
		id, file := c.APNsKeyID, c.APNsKeyFile
		if selected == "development" && (c.APNsSandboxKeyID != "" || c.APNsSandboxKeyFile != "") {
			id, file = c.APNsSandboxKeyID, c.APNsSandboxKeyFile
		}
		if selected == "production" && (c.APNsProductionKeyID != "" || c.APNsProductionKeyFile != "") {
			id, file = c.APNsProductionKeyID, c.APNsProductionKeyFile
		}
		for _, value := range []string{id, file} {
			if value == "" || strings.Contains(value, "REPLACE_") {
				return nil, fmt.Errorf("configure a complete APNs %s key ID and key path", selected)
			}
		}
		result[selected] = apns.Credentials{KeyID: id, KeyFile: file}
	}
	return result, nil
}
func (c Config) NewAPNsProvider() (*apns.Provider, error) {
	keys, err := c.APNsCredentials()
	if err != nil {
		return nil, err
	}
	return apns.NewProvider(c.APNsTeam, c.APNsBundle, keys)
}
