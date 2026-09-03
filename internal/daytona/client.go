package daytona

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Sandbox represents a Daytona sandbox resource.
type Sandbox struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	State    string            `json:"state"`
	Labels   map[string]string `json:"labels"`
	CPU      int               `json:"cpu"`
	Memory   int               `json:"memory"` // GB
	Snapshot string            `json:"snapshot,omitempty"`
}

// Snapshot represents a Daytona snapshot resource.
type Snapshot struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	ImageName string `json:"imageName"`
	State     string `json:"state"`
	CPU       int    `json:"cpu"`
	Mem       int    `json:"mem"`
	Disk      int    `json:"disk"`
}

// Client is an authenticated Daytona REST client.
type Client struct {
	baseURL  string
	adminKey string
	http     *http.Client
}

func NewClient(baseURL, adminKey string) *Client {
	return &Client{
		baseURL:  baseURL,
		adminKey: adminKey,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.adminKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// ErrNotFound reports a definitive 404 from Daytona: the sandbox does not
// exist (deleted, or never existed). Callers use errors.Is to distinguish
// "gone for good" from transient lookup failures.
var ErrNotFound = errors.New("sandbox not found")

func (c *Client) GetSandbox(ctx context.Context, id string) (*Sandbox, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/sandbox/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("daytona GetSandbox %s: %w", id, ErrNotFound)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daytona GetSandbox %s: status %d", id, resp.StatusCode)
	}
	var s Sandbox
	return &s, json.NewDecoder(resp.Body).Decode(&s)
}

// ListSandboxes returns every sandbox visible to the admin key. v0.189+
// paginates the response as {items, nextCursor}; older versions returned a
// flat array. Handle both shapes and walk the cursor when present.
func (c *Client) ListSandboxes(ctx context.Context) ([]Sandbox, error) {
	var all []Sandbox
	cursor := ""
	for {
		// v0.189 caps limit at 200; larger values return 400. Pre-v0.189 ignores.
		path := "/api/sandbox?limit=200"
		if cursor != "" {
			path += "&cursor=" + cursor
		}
		resp, err := c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("daytona ListSandboxes: status %d", resp.StatusCode)
		}
		// v0.189+ envelope.
		var paged struct {
			Items      []Sandbox `json:"items"`
			NextCursor string    `json:"nextCursor"`
		}
		if err := json.Unmarshal(body, &paged); err == nil && paged.Items != nil {
			all = append(all, paged.Items...)
			if paged.NextCursor == "" {
				return all, nil
			}
			cursor = paged.NextCursor
			continue
		}
		// Legacy flat array.
		var flat []Sandbox
		if err := json.Unmarshal(body, &flat); err != nil {
			return nil, fmt.Errorf("daytona ListSandboxes: decode: %w", err)
		}
		return flat, nil
	}
}

func (c *Client) StopSandbox(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/sandbox/"+id+"/stop", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daytona StopSandbox %s: status %d", id, resp.StatusCode)
	}
	return nil
}

// ArchiveSandbox archives a sandbox (backs up container to object storage).
// Archived sandboxes can be restarted later via Daytona's start endpoint,
// unlike stopped sandboxes where the container is removed without a backup.
func (c *Client) ArchiveSandbox(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/sandbox/"+id+"/archive", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("daytona ArchiveSandbox %s: status %d", id, resp.StatusCode)
	}
	return nil
}

// SSHAccess holds the result of creating SSH access for a sandbox.
type SSHAccess struct {
	Token      string `json:"token"`
	ExpiresAt  string `json:"expiresAt"`
	SSHCommand string `json:"sshCommand"`
}

// CreateSSHAccess creates a temporary SSH access token for a sandbox.
func (c *Client) CreateSSHAccess(ctx context.Context, id string) (*SSHAccess, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/sandbox/"+id+"/ssh-access", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daytona CreateSSHAccess %s: status %d", id, resp.StatusCode)
	}
	var a SSHAccess
	return &a, json.NewDecoder(resp.Body).Decode(&a)
}

// WaitStopped polls GetSandbox until the sandbox state is "stopped" (or any
// non-running terminal state) or the context deadline is exceeded.
// Call this after StopSandbox before calling ArchiveSandbox.
func (c *Client) WaitStopped(ctx context.Context, id string) error {
	for {
		s, err := c.GetSandbox(ctx, id)
		if err != nil {
			return err
		}
		switch s.State {
		case "stopped", "archived", "error":
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// GetSnapshot returns a single snapshot by ID (UUID). Returns nil, nil when not found.
func (c *Client) GetSnapshot(ctx context.Context, id string) (*Snapshot, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/snapshots/"+id, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("daytona GetSnapshot %s: status %d", id, resp.StatusCode)
	}
	var s Snapshot
	return &s, json.NewDecoder(resp.Body).Decode(&s)
}

// ListSnapshots returns all Daytona snapshots. v0.189 paginates GET /api/snapshots
// as {items, total, page, totalPages} with a default page size of 100, so walk every
// page rather than returning only the first — otherwise the list silently truncates
// once the registry holds more than one page of snapshots.
func (c *Client) ListSnapshots(ctx context.Context) ([]Snapshot, error) {
	var all []Snapshot
	for page := 1; ; page++ {
		path := fmt.Sprintf("/api/snapshots?limit=100&page=%d", page)
		resp, err := c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("daytona list snapshots: %s", body)
		}
		var paged struct {
			Items      []Snapshot `json:"items"`
			TotalPages int        `json:"totalPages"`
		}
		if err := json.Unmarshal(body, &paged); err != nil {
			return nil, fmt.Errorf("decode snapshots: %w", err)
		}
		all = append(all, paged.Items...)
		if len(paged.Items) == 0 || page >= paged.TotalPages {
			return all, nil
		}
	}
}

// BaseURL returns the configured base URL (used by reverse proxy).
func (c *Client) BaseURL() string { return c.baseURL }

// AdminKey returns the admin key (used by reverse proxy to inject auth).
func (c *Client) AdminKey() string { return c.adminKey }
