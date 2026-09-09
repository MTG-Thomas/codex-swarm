package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// HostConfig is loaded explicitly by a user-owned daemon. Only its filename is
// persisted in service arguments; the host credential never enters argv.
type HostConfig struct {
	URL      string `json:"url"`
	Host     string `json:"host"`
	Token    string `json:"token"`
	Codex    string `json:"codex,omitempty"`
	Journal  string `json:"journal,omitempty"`
	Interval string `json:"interval,omitempty"`
}

func LoadHostConfig(path, statePath string) (HostConfig, error) {
	var c HostConfig
	if !filepath.IsAbs(path) {
		return c, fmt.Errorf("relay config path must be absolute")
	}
	f, err := os.Open(path)
	if err != nil {
		return c, fmt.Errorf("open relay config: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	if !info.Mode().IsRegular() || info.Size() > 65536 {
		return c, fmt.Errorf("relay config must be a regular file no larger than 64 KiB")
	}
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&c); err != nil {
		return c, fmt.Errorf("invalid relay config JSON (contents withheld)")
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		return c, fmt.Errorf("relay config must contain exactly one JSON object")
	}
	if err = (&Client{URL: c.URL, Host: c.Host, Token: c.Token}).Validate(); err != nil {
		return c, err
	}
	if c.Codex == "" {
		c.Codex = "codex"
	}
	if c.Journal == "" {
		c.Journal = filepath.Join(filepath.Dir(statePath), "relay.db")
	}
	if !filepath.IsAbs(c.Journal) {
		return c, fmt.Errorf("relay journal path must be absolute")
	}
	if c.Interval == "" {
		c.Interval = "30s"
	}
	if _, err = c.PollInterval(); err != nil {
		return c, err
	}
	return c, nil
}
func (c HostConfig) PollInterval() (time.Duration, error) {
	d, err := time.ParseDuration(c.Interval)
	if err != nil || d < 5*time.Second || d > time.Hour {
		return 0, fmt.Errorf("relay interval must be between 5s and 1h")
	}
	return d, nil
}

// RunHost shares its caller's cancellation and joins all subprocess work before
// returning. Recoverable transport failures retain the journal and poll again.
func RunHost(ctx context.Context, c HostConfig, out io.Writer) error {
	if err := CheckUser(); err != nil {
		return err
	}
	interval, err := c.PollInterval()
	if err != nil {
		return err
	}
	client := &Client{URL: c.URL, Host: c.Host, Token: c.Token}
	if err = client.Validate(); err != nil {
		return err
	}
	queue := CodexQueue{Binary: c.Codex}
	if err = queue.Check(ctx); err != nil {
		return err
	}
	return runHostLoop(ctx, interval, out, func(ctx context.Context) error {
		j, err := OpenJournal(c.Journal, client.URL, client.Host)
		if err != nil {
			return err
		}
		defer j.Close()
		return (Runner{Host: client.Host, Journal: j, Mailbox: client, Queue: queue}).Once(ctx)
	})
}
func runHostLoop(ctx context.Context, interval time.Duration, out io.Writer, once func(context.Context) error) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		if err := once(ctx); err != nil && !errors.Is(err, context.Canceled) {
			fmt.Fprintf(out, "relay: %v\n", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
}
