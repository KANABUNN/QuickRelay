package apns

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Credentials are selected by environment before opening an HTTP/2 connection.
// Topic-specific keys are supported by the single, explicit bundle topic.
type Credentials struct{ KeyID, KeyFile string }
type Provider struct{ clients map[string]*Client }

func NewProvider(team, topic string, keys map[string]Credentials) (*Provider, error) {
	if len(keys) == 0 {
		return nil, errors.New("configure at least one APNs environment")
	}
	p := &Provider{clients: make(map[string]*Client)}
	for env, key := range keys {
		if env != "development" && env != "production" {
			return nil, errors.New("invalid APNs environment")
		}
		raw, err := os.ReadFile(key.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("cannot read APNs %s key file", env)
		}
		c, err := New(team, key.KeyID, topic, raw)
		if err != nil {
			return nil, fmt.Errorf("APNs %s: %w", env, err)
		}
		p.clients[env] = c
	}
	return p, nil
}
func (p *Provider) Supports(env string) bool {
	if env == "sandbox" {
		env = "development"
	}
	return p.clients[env] != nil
}
func (p *Provider) Send(ctx context.Context, n Request) (Result, error) {
	if n.Environment == "sandbox" {
		n.Environment = "development"
	}
	c := p.clients[n.Environment]
	if c == nil {
		// Do not retry or invalidate a device when its environment is disabled.
		return Result{Status: 403, Reason: "EnvironmentNotConfigured"}, nil
	}
	return c.Send(ctx, n)
}
