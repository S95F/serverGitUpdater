// Package githubhook is a tiny client for GitHub's repo-webhooks REST API,
// just enough to create/update/delete a single hook per repo. It uses
// stdlib net/http to avoid pulling in go-github.
//
// All calls require a token; SSH credentials don't grant API access.
// The minimum scope is `admin:repo_hook` on a classic PAT, or
// "Webhooks: Read and write" on a fine-grained PAT scoped to the repo.
package githubhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.github.com"

// envBaseURL allows tests (and the operator, in a pinch) to point this
// client at a fake or self-hosted GitHub by setting SGU_GITHUB_BASE_URL.
const envBaseURL = "SGU_GITHUB_BASE_URL"

type Hook struct {
	ID     int64    `json:"id"`
	URL    string   `json:"url"`
	Events []string `json:"events"`
	Active bool     `json:"active"`
	Config Config   `json:"config"`
}

type Config struct {
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
	Secret      string `json:"secret,omitempty"`
}

type Payload struct {
	Name   string   `json:"name"`              // always "web"
	Active bool     `json:"active"`
	Events []string `json:"events"`
	Config Config   `json:"config"`
}

// Client speaks to api.github.com.
type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func NewClient() *Client {
	base := defaultBaseURL
	if v := os.Getenv(envBaseURL); v != "" {
		base = v
	}
	return &Client{
		BaseURL: base,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ErrNotFound indicates the resource (typically a hook) doesn't exist.
var ErrNotFound = errors.New("github: not found")

func (c *Client) do(ctx context.Context, method, token, path string, body any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.HTTP.Do(req)
}

func parseError(res *http.Response) error {
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	var ge struct {
		Message          string `json:"message"`
		DocumentationURL string `json:"documentation_url"`
	}
	body, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(body, &ge)
	if ge.Message != "" {
		return fmt.Errorf("github %d: %s", res.StatusCode, ge.Message)
	}
	if len(body) > 0 {
		return fmt.Errorf("github %d: %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	return fmt.Errorf("github %d", res.StatusCode)
}

func (c *Client) Create(ctx context.Context, token, owner, repo string, p Payload) (Hook, error) {
	res, err := c.do(ctx, "POST", token, fmt.Sprintf("/repos/%s/%s/hooks", owner, repo), p)
	if err != nil {
		return Hook{}, err
	}
	if res.StatusCode/100 != 2 {
		return Hook{}, parseError(res)
	}
	defer res.Body.Close()
	var h Hook
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		return Hook{}, err
	}
	return h, nil
}

func (c *Client) Update(ctx context.Context, token, owner, repo string, id int64, p Payload) (Hook, error) {
	res, err := c.do(ctx, "PATCH", token, fmt.Sprintf("/repos/%s/%s/hooks/%d", owner, repo, id), p)
	if err != nil {
		return Hook{}, err
	}
	if res.StatusCode/100 != 2 {
		return Hook{}, parseError(res)
	}
	defer res.Body.Close()
	var h Hook
	if err := json.NewDecoder(res.Body).Decode(&h); err != nil {
		return Hook{}, err
	}
	return h, nil
}

func (c *Client) Delete(ctx context.Context, token, owner, repo string, id int64) error {
	res, err := c.do(ctx, "DELETE", token, fmt.Sprintf("/repos/%s/%s/hooks/%d", owner, repo, id), nil)
	if err != nil {
		return err
	}
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
		res.Body.Close()
		return nil
	}
	return parseError(res)
}

// ParseURL extracts the (owner, repo) from a GitHub clone URL in any of
// the common shapes: https, ssh-style "git@github.com:owner/repo", or
// the longer "ssh://git@github.com/owner/repo". A trailing ".git" or
// "/" is tolerated. ok=false for anything else (incl. non-GitHub).
func ParseURL(cloneURL string) (owner, repo string, ok bool) {
	u := strings.TrimSpace(cloneURL)
	u = strings.TrimSuffix(u, "/")
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return "", "", false
	}
	// SSH shorthand: git@github.com:owner/repo
	if strings.HasPrefix(u, "git@github.com:") {
		path := strings.TrimPrefix(u, "git@github.com:")
		return splitOwnerRepo(path)
	}
	for _, p := range []string{
		"https://github.com/",
		"http://github.com/",
		"ssh://git@github.com/",
		"git+ssh://git@github.com/",
	} {
		if strings.HasPrefix(u, p) {
			return splitOwnerRepo(strings.TrimPrefix(u, p))
		}
	}
	return "", "", false
}

func splitOwnerRepo(path string) (string, string, bool) {
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
