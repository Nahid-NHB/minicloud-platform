// Package sdk provides a thin Go client for the Mini Cloud Platform
// REST API. It is safe to use from any program that needs to talk to
// cloudapi.
package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Client is a thin REST client.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// New returns a Client pointing at baseURL.
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL, HTTP: http.DefaultClient}
}

// Login authenticates with email/password and stores the bearer token.
func (c *Client) Login(ctx context.Context, email, password string) (string, error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("login: %s", string(b))
	}
	var out map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	c.Token = out["token"]
	return c.Token, nil
}

// do performs an authenticated request.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, string(b))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// CreateProject creates a project.
func (c *Client) CreateProject(ctx context.Context, id, desc string) error {
	return c.do(ctx, "POST", "/v1/projects", map[string]any{"id": id, "description": desc}, nil)
}

// CreateWorkload creates a workload.
func (c *Client) CreateWorkload(ctx context.Context, projectID string, w map[string]any) error {
	if w["id"] == nil {
		return errors.New("sdk: workload id required")
	}
	return c.do(ctx, "POST", "/v1/workloads?project_id="+projectID, w, nil)
}

// ListWorkloads lists workloads in a project.
func (c *Client) ListWorkloads(ctx context.Context, projectID string, out any) error {
	return c.do(ctx, "GET", "/v1/workloads?project_id="+projectID, nil, out)
}

// CreateBucket creates an S3-compatible bucket.
func (c *Client) CreateBucket(ctx context.Context, id string) error {
	return c.do(ctx, "POST", "/v1/buckets", map[string]any{"id": id}, nil)
}

// Chat sends a chat-completion request.
func (c *Client) Chat(ctx context.Context, model, prompt string, out any) error {
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
