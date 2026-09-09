// Package relay implements the opt-in cross-host mailbox. Codex execution and
// credentials stay on each host; the coordinator owns only shared delivery state.
package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var hostPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func NewID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	} // crypto/rand is infallible on supported Go platforms.
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

type Client struct {
	URL, Host, Token string
	HTTP             *http.Client
}

func FromEnv() (*Client, error) {
	c := &Client{URL: strings.TrimRight(os.Getenv("CODEX_SWARM_RELAY_URL"), "/"), Host: os.Getenv("CODEX_SWARM_HOST_ID"), Token: os.Getenv("CODEX_SWARM_RELAY_TOKEN")}
	return c, c.Validate()
}
func (c *Client) Validate() error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return fmt.Errorf("relay URL must be an origin without credentials, path, query or fragment")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "::1" || u.Hostname() == "localhost")) {
		return fmt.Errorf("relay requires HTTPS (HTTP allowed only on loopback for tests)")
	}
	if !hostPattern.MatchString(c.Host) || len(c.Token) < 16 {
		return fmt.Errorf("relay requires stable CODEX_SWARM_HOST_ID and CODEX_SWARM_RELAY_TOKEN (at least 16 characters)")
	}
	return nil
}
func (c *Client) Call(ctx context.Context, method, path string, input, output any) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var data []byte
	if input != nil {
		var err error
		data, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.URL, "/")+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("X-Swarm-Host", c.Host)
	req.Header.Set("Content-Type", "application/json")
	hc := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if c.HTTP != nil {
		hc = *c.HTTP
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	r, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("relay request %s %s failed: %w", method, path, err)
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return fmt.Errorf("relay response exceeds 1 MiB")
	}
	if r.StatusCode != 200 {
		return fmt.Errorf("relay %s %s returned HTTP %d (request IDs can be replayed safely)", method, path, r.StatusCode)
	}
	if output != nil {
		if err = json.Unmarshal(b, output); err != nil {
			return fmt.Errorf("decode relay response: %w", err)
		}
	}
	return nil
}

type Message struct {
	ID           string `json:"id"`
	SenderHost   string `json:"sender_host"`
	TargetHost   string `json:"target_host"`
	ThreadID     string `json:"thread_id"`
	Prompt       string `json:"prompt"`
	State        string `json:"state"`
	AttemptID    string `json:"attempt_id"`
	SubmissionID string `json:"submission_id"`
}
type Receipt struct {
	RequestID    string `json:"request_id"`
	MessageID    string `json:"message_id"`
	State        string `json:"state"`
	SubmissionID string `json:"submission_id"`
	Evidence     string `json:"evidence"`
}

func (c *Client) Claim(ctx context.Context, id string) (*Message, error) {
	var result struct {
		Message *Message `json:"message"`
	}
	err := c.Call(ctx, "POST", "/v1/claim", map[string]string{"request_id": id}, &result)
	return result.Message, err
}
func (c *Client) Report(ctx context.Context, r Receipt) error {
	return c.Call(ctx, "POST", "/v1/receipts", r, nil)
}

// Ready avoids writing an empty claim on every idle polling interval.
func (c *Client) Ready(ctx context.Context) (bool, error) {
	var r struct {
		Ready bool `json:"ready"`
	}
	err := c.Call(ctx, "GET", "/v1/ready", nil, &r)
	return r.Ready, err
}

func (c *Client) Inspect(ctx context.Context, id string) (*Message, error) {
	var m Message
	err := c.Call(ctx, "GET", "/v1/messages/"+url.PathEscape(id), nil, &m)
	return &m, err
}
