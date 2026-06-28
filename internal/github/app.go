package github

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/MTG-Thomas/codex-swarm/internal/readiness"
)

const defaultAPIURL = "https://api.github.com"

type AppConfig struct {
	AppID          int64
	Issuer         string
	InstallationID int64
	PrivateKeyPEM  []byte
	APIURL         string
	HTTPClient     *http.Client
	Now            func() time.Time
}

type AppIssueMetadataProvider struct {
	cfg AppConfig
	key *rsa.PrivateKey
}

func NewAppIssueMetadataProvider(cfg AppConfig) (*AppIssueMetadataProvider, error) {
	if cfg.AppID <= 0 && strings.TrimSpace(cfg.Issuer) == "" {
		return nil, fmt.Errorf("github app id or client id is required")
	}
	cfg.Issuer = strings.TrimSpace(cfg.Issuer)
	key, err := parsePrivateKey(cfg.PrivateKeyPEM)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.APIURL) == "" {
		cfg.APIURL = defaultAPIURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &AppIssueMetadataProvider{cfg: cfg, key: key}, nil
}

func (p *AppIssueMetadataProvider) IssueMetadata(ctx context.Context, issue string) (readiness.Issue, error) {
	ref, err := ParseIssueRef(issue)
	if err != nil {
		return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata for %q: %w", issue, err)
	}
	installationID := p.cfg.InstallationID
	if installationID == 0 {
		installationID, err = p.repositoryInstallationID(ctx, ref)
		if err != nil {
			return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata for %s: %w", ref.String(), err)
		}
	}
	token, err := p.installationToken(ctx, installationID)
	if err != nil {
		return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata for %s: %w", ref.String(), err)
	}
	var metadata issueMetadata
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo), ref.Number)
	if err := p.githubJSON(ctx, http.MethodGet, path, token, nil, &metadata); err != nil {
		return readiness.Issue{}, fmt.Errorf("read GitHub issue metadata for %s: %w", ref.String(), err)
	}
	return readiness.Issue{
		Ref:   ref.String(),
		Title: metadata.Title,
		Body:  metadata.Body,
	}, nil
}

func (p *AppIssueMetadataProvider) repositoryInstallationID(ctx context.Context, ref IssueRef) (int64, error) {
	jwt, err := p.jwt()
	if err != nil {
		return 0, err
	}
	var response struct {
		ID int64 `json:"id"`
	}
	path := fmt.Sprintf("/repos/%s/%s/installation", url.PathEscape(ref.Owner), url.PathEscape(ref.Repo))
	if err := p.githubJSON(ctx, http.MethodGet, path, jwt, nil, &response); err != nil {
		return 0, fmt.Errorf("discover GitHub App installation for %s/%s: %w", ref.Owner, ref.Repo, err)
	}
	if response.ID <= 0 {
		return 0, fmt.Errorf("discover GitHub App installation for %s/%s: missing installation id", ref.Owner, ref.Repo)
	}
	return response.ID, nil
}

func (p *AppIssueMetadataProvider) installationToken(ctx context.Context, installationID int64) (string, error) {
	jwt, err := p.jwt()
	if err != nil {
		return "", err
	}
	var response struct {
		Token string `json:"token"`
	}
	path := fmt.Sprintf("/app/installations/%d/access_tokens", installationID)
	if err := p.githubJSON(ctx, http.MethodPost, path, jwt, []byte("{}"), &response); err != nil {
		return "", fmt.Errorf("create GitHub App installation token: %w", err)
	}
	if strings.TrimSpace(response.Token) == "" {
		return "", fmt.Errorf("create GitHub App installation token: missing token")
	}
	return response.Token, nil
}

func (p *AppIssueMetadataProvider) jwt() (string, error) {
	now := p.cfg.Now().UTC()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": p.issuer(),
	})
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign GitHub App JWT: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (p *AppIssueMetadataProvider) issuer() string {
	if p.cfg.Issuer != "" {
		return p.cfg.Issuer
	}
	return strconv.FormatInt(p.cfg.AppID, 10)
}

func (p *AppIssueMetadataProvider) githubJSON(ctx context.Context, method, path, token string, body []byte, target any) error {
	base := strings.TrimRight(p.cfg.APIURL, "/")
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "codex-swarm")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := p.cfg.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("github api returned %s: %s", resp.Status, compactBody(resp.Body))
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func parsePrivateKey(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("parse GitHub App private key: PEM block not found")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse GitHub App private key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("parse GitHub App private key: expected RSA private key")
	}
	return key, nil
}

func compactBody(r io.Reader) string {
	data, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
