// Package client is a small client for the public PeerNode API (https://peernode.cloud/api/v1).
// It depends on nothing but the documented HTTP API, so it can live outside the platform repository.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client calls the PeerNode API with an API token (Authorization: Bearer pnk_...).
type Client struct {
	Endpoint  string // e.g. https://peernode.cloud
	Token     string
	HTTP      *http.Client
	UserAgent string
}

// New returns a client for endpoint (without the /api/v1 suffix).
func New(endpoint, token, userAgent string) *Client {
	return &Client{
		Endpoint:  strings.TrimRight(endpoint, "/"),
		Token:     token,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		UserAgent: userAgent,
	}
}

// Error is an API error: {"error": {"code", "message"}} with the HTTP status.
type Error struct {
	Status  int
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("PeerNode API: HTTP %d", e.Status)
	}
	return fmt.Sprintf("PeerNode API: %s (%s, HTTP %d)", e.Message, e.Code, e.Status)
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}

// Do sends a request with an optional JSON body and decodes a JSON response into out (if not nil).
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.Endpoint+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var env struct {
			Error *Error `json:"error"`
		}
		apiErr := &Error{Status: resp.StatusCode}
		if json.Unmarshal(data, &env) == nil && env.Error != nil {
			apiErr.Code, apiErr.Message = env.Error.Code, env.Error.Message
		}
		return apiErr
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("PeerNode API: decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// Kubernetes cluster spec (docs/API.md "Kubernetes clusters").

type AutopilotSpec struct {
	MaxCPU      float64 `json:"max_cpu"`
	MaxMemoryGB float64 `json:"max_memory_gb"`
	MaxDiskGB   int64   `json:"max_disk_gb"`
}

type AutoSpec struct {
	CPU            float64 `json:"cpu"`
	MemoryGB       float64 `json:"memory_gb"`
	DiskGB         int64   `json:"disk_gb"`
	MinPodCPU      float64 `json:"min_pod_cpu,omitempty"`
	MinPodMemoryGB float64 `json:"min_pod_memory_gb,omitempty"`
}

type PoolSpec struct {
	Name     string  `json:"name"`
	Count    int64   `json:"count"`
	CPU      float64 `json:"cpu"`
	MemoryGB float64 `json:"memory_gb"`
	DiskGB   int64   `json:"disk_gb"`
}

type Spec struct {
	Mode      string         `json:"mode"`
	Autopilot *AutopilotSpec `json:"autopilot,omitempty"`
	Auto      *AutoSpec      `json:"auto,omitempty"`
	Pools     []PoolSpec     `json:"pools,omitempty"`
}

type Size struct {
	CPUMilli    int64 `json:"cpu_milli"`
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`
}

type Cluster struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Status        string   `json:"status"`
	StatusMessage string   `json:"status_message"`
	HasKubeconfig bool     `json:"has_kubeconfig"`
	PublicHosts   []string `json:"public_hosts"`
	PublicMessage string   `json:"public_message"`
	Spec          Spec     `json:"spec"`
	Size          Size     `json:"size"`
	HourlyMicro   int64    `json:"hourly_micro"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

// Cluster statuses.
const (
	StatusProvisioning = "provisioning"
	StatusReady        = "ready"
	StatusFailed       = "failed"
	StatusDeleting     = "deleting"
)

type clusterEnvelope struct {
	Cluster *Cluster `json:"cluster"`
}

// CreateCluster creates a cluster (POST /api/v1/k8s/clusters).
func (c *Client) CreateCluster(ctx context.Context, name string, spec Spec) (*Cluster, error) {
	body := map[string]any{"name": name, "mode": spec.Mode}
	if spec.Autopilot != nil {
		body["autopilot"] = spec.Autopilot
	}
	if spec.Auto != nil {
		body["auto"] = spec.Auto
	}
	if len(spec.Pools) > 0 {
		body["pools"] = spec.Pools
	}
	var env clusterEnvelope
	if err := c.Do(ctx, http.MethodPost, "/api/v1/k8s/clusters", body, &env); err != nil {
		return nil, err
	}
	return env.Cluster, nil
}

// GetCluster reads a cluster by id.
func (c *Client) GetCluster(ctx context.Context, id string) (*Cluster, error) {
	var env clusterEnvelope
	if err := c.Do(ctx, http.MethodGet, "/api/v1/k8s/clusters/"+url.PathEscape(id), nil, &env); err != nil {
		return nil, err
	}
	if env.Cluster == nil {
		return nil, &Error{Status: http.StatusNotFound, Code: "not_found", Message: "cluster not found"}
	}
	return env.Cluster, nil
}

// ListClusters lists the account's clusters.
func (c *Client) ListClusters(ctx context.Context) ([]Cluster, error) {
	var env struct {
		Clusters []Cluster `json:"clusters"`
	}
	if err := c.Do(ctx, http.MethodGet, "/api/v1/k8s/clusters", nil, &env); err != nil {
		return nil, err
	}
	return env.Clusters, nil
}

// UpdateSpec resizes a cluster (PUT /api/v1/k8s/clusters/{id}/spec); the mode cannot change.
func (c *Client) UpdateSpec(ctx context.Context, id string, spec Spec) (*Cluster, error) {
	var env clusterEnvelope
	if err := c.Do(ctx, http.MethodPut, "/api/v1/k8s/clusters/"+url.PathEscape(id)+"/spec", spec, &env); err != nil {
		return nil, err
	}
	return env.Cluster, nil
}

// DeleteCluster starts deleting a cluster; it disappears once its host objects are gone.
func (c *Client) DeleteCluster(ctx context.Context, id string) error {
	return c.Do(ctx, http.MethodDelete, "/api/v1/k8s/clusters/"+url.PathEscape(id), nil, nil)
}

// Kubeconfig is a freshly issued kubeconfig.
type Kubeconfig struct {
	Kubeconfig string `json:"kubeconfig"`
	Filename   string `json:"filename"`
}

// IssueKubeconfig issues a kubeconfig; this revokes the cluster's previous kubeconfig token.
func (c *Client) IssueKubeconfig(ctx context.Context, id string) (*Kubeconfig, error) {
	var kc Kubeconfig
	if err := c.Do(ctx, http.MethodPost, "/api/v1/k8s/clusters/"+url.PathEscape(id)+"/kubeconfig", nil, &kc); err != nil {
		return nil, err
	}
	return &kc, nil
}

// WaitCluster polls until the cluster leaves provisioning (or, with gone, until it no longer exists).
func (c *Client) WaitCluster(ctx context.Context, id string, gone bool, every time.Duration) (*Cluster, error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		cl, err := c.GetCluster(ctx, id)
		switch {
		case gone && IsNotFound(err):
			return nil, nil
		case err != nil:
			return nil, err
		case !gone && cl.Status == StatusReady:
			return cl, nil
		case !gone && cl.Status == StatusFailed:
			return cl, fmt.Errorf("cluster %s failed: %s", cl.Name, cl.StatusMessage)
		case !gone && cl.Status == StatusDeleting:
			return cl, fmt.Errorf("cluster %s is being deleted", cl.Name)
		}
		select {
		case <-ctx.Done():
			if cl != nil {
				return cl, fmt.Errorf("timed out waiting for cluster %s (status %s): %w", cl.Name, cl.Status, ctx.Err())
			}
			return nil, ctx.Err()
		case <-t.C:
		}
	}
}
