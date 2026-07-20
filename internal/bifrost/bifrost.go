package bifrost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
)

const DefaultBasePath = "/api/workspace-repo-changesets"

type CommandRunner interface {
	Run(context.Context, string, []string, []byte, []string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args []string, stdin []byte, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("run %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type Conflict struct {
	Reason           string   `json:"reason,omitempty"`
	CurrentRevision  string   `json:"current_revision,omitempty"`
	BaseRevision     string   `json:"base_revision,omitempty"`
	ConflictingPaths []string `json:"conflicting_paths,omitempty"`
}

type APIError struct {
	Status   int      `json:"status,omitempty"`
	Detail   string   `json:"detail,omitempty"`
	Conflict Conflict `json:"conflict,omitempty"`
}

func (e *APIError) Error() string {
	if e.Detail != "" {
		return e.Detail
	}
	return fmt.Sprintf("Bifrost API failed with status %d", e.Status)
}

type Changeset struct {
	ID           string          `json:"id"`
	State        string          `json:"status"`
	Scope        string          `json:"scope,omitempty"`
	WorkerID     string          `json:"worker_id,omitempty"`
	BaseRevision string          `json:"base_revision,omitempty"`
	CommitSHA    string          `json:"commit_sha,omitempty"`
	Validation   json.RawMessage `json:"validation,omitempty"`
	Diff         json.RawMessage `json:"diff,omitempty"`
}

type ValidationResult struct {
	Valid                bool            `json:"valid"`
	Diagnostics          json.RawMessage `json:"diagnostics,omitempty"`
	PendingDeactivations json.RawMessage `json:"pending_deactivations,omitempty"`
	ValidatedRevision    string          `json:"validated_revision,omitempty"`
}

type FileMutation struct {
	Path              string `json:"path"`
	Operation         string `json:"operation"`
	ContentBase64     string `json:"content_base64,omitempty"`
	ExpectedHash      string `json:"expected_hash,omitempty"`
	ForceDeactivation bool   `json:"force_deactivation,omitempty"`
}

type InspectResult struct {
	Scope     string          `json:"scope"`
	Revision  string          `json:"revision,omitempty"`
	Dirty     bool            `json:"dirty"`
	GitStatus json.RawMessage `json:"git_status,omitempty"`
	Ready     bool            `json:"ready"`
	Blockers  []string        `json:"blockers,omitempty"`
}

type Client struct {
	Runner                   CommandRunner
	Binary, BasePath, Target string
}

func NewClient(r CommandRunner) *Client {
	return &Client{Runner: r, Binary: "bifrost", BasePath: DefaultBasePath}
}

func (c *Client) call(ctx context.Context, method, path string, body any, out any) error {
	if c.Runner == nil {
		return errors.New("Bifrost command runner is required")
	}
	bin := c.Binary
	if bin == "" {
		bin = "bifrost"
	}
	base := c.BasePath
	if base == "" {
		base = DefaultBasePath
	}
	path = strings.TrimRight(base, "/") + path
	args := []string{"api", method, path}
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		args = append(args, string(payload))
	}
	env := []string{}
	if c.Target != "" {
		env = append(env, "BIFROST_API_URL="+c.Target)
	}
	raw, err := c.Runner.Run(ctx, bin, args, nil, env)
	if err != nil {
		var envelope struct {
			Detail json.RawMessage `json:"detail"`
		}
		if json.Unmarshal(raw, &envelope) == nil && len(envelope.Detail) > 0 {
			apiErr := &APIError{}
			if json.Unmarshal(envelope.Detail, &apiErr.Conflict) == nil && apiErr.Conflict.Reason != "" {
				apiErr.Detail = apiErr.Conflict.Reason
				return apiErr
			}
			_ = json.Unmarshal(envelope.Detail, &apiErr.Detail)
			if apiErr.Detail != "" {
				return apiErr
			}
		}
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode Bifrost response: %w", err)
	}
	return nil
}

func (c *Client) Inspect(ctx context.Context, scope string) (InspectResult, error) {
	var v InspectResult
	err := c.call(ctx, "GET", "/state?scope="+url.QueryEscape(scope), nil, &v)
	return v, err
}
func (c *Client) Begin(ctx context.Context, scope, baseRevision, title, worker string) (Changeset, error) {
	body := map[string]any{"scope": scope}
	if baseRevision != "" {
		body["base_revision"] = baseRevision
	}
	if title != "" {
		body["title"] = title
	}
	if worker != "" {
		body["worker_id"] = worker
	}
	var v Changeset
	err := c.call(ctx, "POST", "", body, &v)
	return v, err
}
func (c *Client) Get(ctx context.Context, id string) (Changeset, error) {
	var v Changeset
	err := c.call(ctx, "GET", "/"+url.PathEscape(id), nil, &v)
	return v, err
}
func (c *Client) Stage(ctx context.Context, id string, mutation FileMutation) (Changeset, error) {
	var v Changeset
	err := c.call(ctx, "POST", "/"+url.PathEscape(id)+"/files", mutation, &v)
	return v, err
}
func (c *Client) Diff(ctx context.Context, id string) (json.RawMessage, error) {
	var v json.RawMessage
	err := c.call(ctx, "GET", "/"+url.PathEscape(id)+"/diff", nil, &v)
	return v, err
}
func (c *Client) Validate(ctx context.Context, id string) (ValidationResult, error) {
	var v ValidationResult
	err := c.call(ctx, "POST", "/"+url.PathEscape(id)+"/validate", map[string]any{}, &v)
	return v, err
}
func (c *Client) Commit(ctx context.Context, id, message string, push bool) (Changeset, error) {
	var v Changeset
	err := c.call(ctx, "POST", "/"+url.PathEscape(id)+"/activate", map[string]any{"commit_message": message, "push": push}, &v)
	return v, err
}
func (c *Client) Abort(ctx context.Context, id string) (Changeset, error) {
	var v Changeset
	err := c.call(ctx, "POST", "/"+url.PathEscape(id)+"/abort", map[string]any{}, &v)
	return v, err
}

type Record struct {
	ID                string          `json:"id"`
	WorkerID          string          `json:"worker_id"`
	Target            string          `json:"target,omitempty"`
	Scope             string          `json:"scope"`
	BaseRevision      string          `json:"base_revision,omitempty"`
	RemoteChangesetID string          `json:"remote_changeset_id"`
	State             string          `json:"state"`
	Validation        json.RawMessage `json:"validation,omitempty"`
	CommitSHA         string          `json:"commit_sha,omitempty"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

type RecordStore interface {
	SaveBifrostChangeset(Record) error
	GetBifrostChangeset(string) (Record, error)
	ListBifrostChangesets() ([]Record, error)
}

type Service struct {
	Client *Client
	Store  RecordStore
	Now    func() time.Time
}

func (s Service) Begin(ctx context.Context, scope, base, title, worker string) (Record, error) {
	remote, err := s.Client.Begin(ctx, scope, base, title, worker)
	if err != nil {
		return Record{}, err
	}
	now := time.Now()
	if s.Now != nil {
		now = s.Now()
	}
	r := Record{ID: remote.ID, WorkerID: worker, Target: s.Client.Target, Scope: scope, BaseRevision: remote.BaseRevision, RemoteChangesetID: remote.ID, State: remote.State, CreatedAt: now, UpdatedAt: now}
	if r.BaseRevision == "" {
		r.BaseRevision = base
	}
	if s.Store != nil {
		if err := s.Store.SaveBifrostChangeset(r); err != nil {
			_, abortErr := s.Client.Abort(ctx, remote.ID)
			if abortErr != nil {
				return Record{}, fmt.Errorf("record Bifrost changeset %s: %w; compensating abort also failed: %v", r.ID, err, abortErr)
			}
			return Record{}, fmt.Errorf("record Bifrost changeset %s: %w; remote changeset was aborted", r.ID, err)
		}
	}
	return r, nil
}

func (s Service) selectRecordedTarget(r Record) error {
	if r.Target == "" {
		return nil
	}
	if s.Client.Target != "" && s.Client.Target != r.Target {
		return fmt.Errorf("Bifrost target mismatch: changeset %s belongs to %s, not %s", r.ID, r.Target, s.Client.Target)
	}
	s.Client.Target = r.Target
	return nil
}

func (s Service) sync(ctx context.Context, id, action, message string, push bool) (Record, error) {
	r, err := s.Store.GetBifrostChangeset(id)
	if err != nil {
		return Record{}, err
	}
	if err := s.selectRecordedTarget(r); err != nil {
		return Record{}, err
	}
	var remote Changeset
	switch action {
	case "show":
		remote, err = s.Client.Get(ctx, r.RemoteChangesetID)
	case "validate":
		var validation ValidationResult
		validation, err = s.Client.Validate(ctx, r.RemoteChangesetID)
		if err == nil {
			r.Validation, err = json.Marshal(validation)
			if validation.Valid {
				r.State = "validated"
			} else {
				r.State = "staged"
			}
		}
	case "commit":
		remote, err = s.Client.Commit(ctx, r.RemoteChangesetID, message, push)
	case "abort":
		remote, err = s.Client.Abort(ctx, r.RemoteChangesetID)
	default:
		return Record{}, fmt.Errorf("unknown action %q", action)
	}
	if err != nil {
		return Record{}, err
	}
	if action != "validate" {
		r.State = remote.State
		r.Validation = remote.Validation
		r.CommitSHA = remote.CommitSHA
	}
	r.UpdatedAt = time.Now()
	if s.Now != nil {
		r.UpdatedAt = s.Now()
	}
	if err := s.Store.SaveBifrostChangeset(r); err != nil {
		return Record{}, err
	}
	return r, nil
}

func (s Service) Diff(ctx context.Context, id string) (json.RawMessage, error) {
	r, err := s.Store.GetBifrostChangeset(id)
	if err != nil {
		return nil, err
	}
	if err := s.selectRecordedTarget(r); err != nil {
		return nil, err
	}
	return s.Client.Diff(ctx, r.RemoteChangesetID)
}
func (s Service) Stage(ctx context.Context, id string, mutation FileMutation) (Record, error) {
	r, err := s.Store.GetBifrostChangeset(id)
	if err != nil {
		return Record{}, err
	}
	if err := s.selectRecordedTarget(r); err != nil {
		return Record{}, err
	}
	remote, err := s.Client.Stage(ctx, r.RemoteChangesetID, mutation)
	if err != nil {
		return Record{}, err
	}
	r.State = remote.State
	r.UpdatedAt = time.Now()
	if s.Now != nil {
		r.UpdatedAt = s.Now()
	}
	if err := s.Store.SaveBifrostChangeset(r); err != nil {
		return Record{}, err
	}
	return r, nil
}
func (s Service) Show(ctx context.Context, id string) (Record, error) {
	return s.sync(ctx, id, "show", "", false)
}
func (s Service) Validate(ctx context.Context, id string) (Record, error) {
	return s.sync(ctx, id, "validate", "", false)
}
func (s Service) Commit(ctx context.Context, id, msg string, push bool) (Record, error) {
	return s.sync(ctx, id, "commit", msg, push)
}
func (s Service) Abort(ctx context.Context, id string) (Record, error) {
	return s.sync(ctx, id, "abort", "", false)
}
