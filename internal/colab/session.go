// Package colab provides a client for the Google Colab runtime API and the
// Jupyter kernel protocol that Colab exposes over WebSocket.
package colab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	baseURL     = "https://colab.research.google.com"
	tunEndpoint = "/tun/m"
	xssiPrefix  = ")]}'\n"
)

// RuntimeProxyInfo holds the short-lived token and base URL returned by the
// assign endpoint. These are used to connect to the Jupyter kernel — they are
// NOT the OAuth2 bearer token.
type RuntimeProxyInfo struct {
	Token              string `json:"token"`
	TokenExpiresInSecs int    `json:"tokenExpiresInSeconds"`
	URL                string `json:"url"`
}

// AssignmentInfo is what Assign returns after a successful runtime allocation.
type AssignmentInfo struct {
	Endpoint         string
	RuntimeProxyInfo RuntimeProxyInfo
}

// getAssignmentResponse is the first-step response: contains an XSRF token
// that must be echoed back in the POST.
type getAssignmentResponse struct {
	Acc     string `json:"acc"`
	Nbh     string `json:"nbh"`
	Token   string `json:"token"`
	Variant string `json:"variant"`
}

// postAssignmentResponse is the second-step response: contains the actual
// runtime proxy info.
type postAssignmentResponse struct {
	Accelerator      string           `json:"accelerator"`
	Endpoint         string           `json:"endpoint"`
	RuntimeProxyInfo RuntimeProxyInfo `json:"runtimeProxyInfo"`
	Variant          int              `json:"variant"`
}

// existingAssignment is returned by the GET when a runtime is already live for
// this notebook hash.
type existingAssignment struct {
	Endpoint         string           `json:"endpoint"`
	RuntimeProxyInfo RuntimeProxyInfo `json:"runtimeProxyInfo"`
}

// Client interacts with the Colab backend API.
type Client struct {
	http *http.Client
}

// New creates a Colab client backed by the given authenticated HTTP client.
func New(httpClient *http.Client) *Client {
	return &Client{http: httpClient}
}

// Assign allocates (or re-uses) a Colab runtime. notebookHash is an opaque
// identifier for the notebook — pass a random UUID hex string for new
// one-shot sessions. variant and accelerator are optional ("" for defaults).
//
// The flow mirrors the official google-colab-cli:
//  1. GET /tun/m/assign?nbh=<hash>&authuser=0
//     → either an existing assignment (done) or an XSRF token for step 2
//  2. POST /tun/m/assign?nbh=<hash>&authuser=0  with X-Goog-Colab-Token
//     → new assignment with runtime proxy info
func (c *Client) Assign(ctx context.Context, notebookHash, variant, accelerator string) (*AssignmentInfo, error) {
	assignURL := c.buildAssignURL(notebookHash, variant, accelerator)

	// Step 1: GET — check for an existing assignment or obtain the XSRF token.
	body, err := c.issueRequest(ctx, http.MethodGet, assignURL, nil)
	if err != nil {
		return nil, fmt.Errorf("assign GET: %w", err)
	}

	// Try to decode as an existing assignment first (has "endpoint" field).
	var existing existingAssignment
	if err := json.Unmarshal(body, &existing); err == nil && existing.Endpoint != "" {
		return &AssignmentInfo{
			Endpoint:         existing.Endpoint,
			RuntimeProxyInfo: existing.RuntimeProxyInfo,
		}, nil
	}

	// Otherwise it should be a first-step response with an XSRF token.
	var step1 getAssignmentResponse
	if err := json.Unmarshal(body, &step1); err != nil || step1.Token == "" {
		return nil, fmt.Errorf("assign GET: unexpected response body: %s", string(body))
	}

	// Step 2: POST — send the XSRF token back to claim the assignment.
	extraHeaders := map[string]string{
		"X-Goog-Colab-Token": step1.Token,
	}
	body, err = c.issueRequest(ctx, http.MethodPost, assignURL, extraHeaders)
	if err != nil {
		// HTTP 412 means the account has reached the maximum number of
		// concurrent runtimes. Surface a clear message instead of raw HTML.
		if strings.Contains(err.Error(), "HTTP 412") {
			return nil, fmt.Errorf("too many active Colab runtimes on this account — " +
				"go to https://colab.research.google.com and stop an existing runtime, then retry")
		}
		if strings.Contains(err.Error(), "HTTP 400") {
			return nil, fmt.Errorf("backend rejected the requested accelerator — " +
				"you may not have quota for it on this account; try a different accelerator or omit --accelerator for CPU")
		}
		return nil, fmt.Errorf("assign POST: %w", err)
	}

	var step2 postAssignmentResponse
	if err := json.Unmarshal(body, &step2); err != nil || step2.Endpoint == "" {
		return nil, fmt.Errorf("assign POST: unexpected response body: %s", string(body))
	}

	return &AssignmentInfo{
		Endpoint:         step2.Endpoint,
		RuntimeProxyInfo: step2.RuntimeProxyInfo,
	}, nil
}

// ActiveRuntime describes one running Colab assignment returned by
// ListAssignments.
type ActiveRuntime struct {
	Endpoint    string
	Accelerator string
	URL         string
	ProxyToken  string
}

// ListAssignments returns all currently active runtimes for the account.
func (c *Client) ListAssignments(ctx context.Context) ([]*ActiveRuntime, error) {
	url := fmt.Sprintf("%s%s/assignments?authuser=0", baseURL, tunEndpoint)
	body, err := c.issueRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("list assignments: %w", err)
	}

	var resp struct {
		Assignments []struct {
			Endpoint         string           `json:"endpoint"`
			Accelerator      string           `json:"accelerator"`
			RuntimeProxyInfo RuntimeProxyInfo `json:"runtimeProxyInfo"`
		} `json:"assignments"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("parse assignments: %w", err)
	}

	out := make([]*ActiveRuntime, 0, len(resp.Assignments))
	for _, a := range resp.Assignments {
		out = append(out, &ActiveRuntime{
			Endpoint:    a.Endpoint,
			Accelerator: a.Accelerator,
			URL:         a.RuntimeProxyInfo.URL,
			ProxyToken:  a.RuntimeProxyInfo.Token,
		})
	}
	return out, nil
}

// AssignmentFromEndpoint looks up an existing runtime by endpoint ID and
// returns an AssignmentInfo so it can be used with CreateAndConnectKernel.
func (c *Client) AssignmentFromEndpoint(ctx context.Context, endpoint string) (*AssignmentInfo, error) {
	runtimes, err := c.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	for _, r := range runtimes {
		if r.Endpoint == endpoint {
			return &AssignmentInfo{
				Endpoint: r.Endpoint,
				RuntimeProxyInfo: RuntimeProxyInfo{
					Token: r.ProxyToken,
					URL:   r.URL,
				},
			}, nil
		}
	}
	return nil, fmt.Errorf("no active runtime found with endpoint %q — run 'colab sessions' to list active runtimes", endpoint)
}

// Unassign releases a runtime by its endpoint identifier.
func (c *Client) Unassign(ctx context.Context, endpoint string) error {
	getURL := fmt.Sprintf("%s%s/unassign/%s?authuser=0", baseURL, tunEndpoint, endpoint)

	body, err := c.issueRequest(ctx, http.MethodGet, getURL, nil)
	if err != nil {
		return fmt.Errorf("unassign GET: %w", err)
	}

	var tokenResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil || tokenResp.Token == "" {
		return fmt.Errorf("unassign GET: unexpected response: %s", string(body))
	}

	postURL := fmt.Sprintf("%s%s/unassign/%s?authuser=0", baseURL, tunEndpoint, endpoint)
	_, err = c.issueRequest(ctx, http.MethodPost, postURL, map[string]string{
		"X-Goog-Colab-Token": tokenResp.Token,
	})
	return err
}

// KeepAlive pings the runtime tunnel to reset its idle timer.
func (c *Client) KeepAlive(ctx context.Context, endpoint string) error {
	keepAliveURL := fmt.Sprintf("%s%s/%s/keep-alive/?authuser=0", baseURL, tunEndpoint, endpoint)
	_, err := c.issueRequest(ctx, http.MethodGet, keepAliveURL, map[string]string{
		"X-Colab-Tunnel": "Google",
	})
	return err
}

// KernelWebSocketURL returns the WebSocket URL for the Jupyter kernel channel.
// kernelID is the UUID returned when a kernel was created via the Jupyter API.
// sessionID is a random hex UUID that identifies this client session; it is
// required by the Colab tunnel and must also be embedded in every message header.
func (info *AssignmentInfo) KernelWebSocketURL(kernelID, sessionID string) string {
	base := strings.TrimRight(info.RuntimeProxyInfo.URL, "/")
	// Convert https:// to wss://.
	base = strings.Replace(base, "https://", "wss://", 1)
	base = strings.Replace(base, "http://", "ws://", 1)
	token := url.QueryEscape(info.RuntimeProxyInfo.Token)
	sid := url.QueryEscape(sessionID)
	// session_id must come first; the Colab tunnel uses it to multiplex channels.
	return fmt.Sprintf("%s/api/kernels/%s/channels?session_id=%s&token=%s&colab-runtime-proxy-token=%s",
		base, kernelID, sid, token, token)
}

// JupyterBaseURL returns the http(s) base URL for Jupyter REST calls.
func (info *AssignmentInfo) JupyterBaseURL() string {
	return strings.TrimRight(info.RuntimeProxyInfo.URL, "/")
}

// buildAssignURL constructs the assign endpoint URL with optional variant /
// accelerator query parameters.
//
// accelerator is a case-insensitive GPU/TPU name (T4, L4, A100, H100, G4,
// V5E1, V6E1) or "" for CPU. variant is inferred automatically when blank:
// GPU accelerators → "GPU", TPU accelerators → "TPU", CPU → "DEFAULT".
// The official CLI always sends both parameters together.
func (c *Client) buildAssignURL(notebookHash, variant, accelerator string) string {
	// Normalise accelerator to the API's expected casing.
	accelUpper := strings.ToUpper(strings.TrimSpace(accelerator))

	// Map user-friendly names to API values.
	accelMap := map[string]string{
		"T4": "T4", "L4": "L4", "A100": "A100", "H100": "H100", "G4": "G4",
		"A100-80GB": "A100", // alias
		"V5E1":      "V5E1", "V6E1": "V6E1",
	}
	apiAccel := "NONE"
	if v, ok := accelMap[accelUpper]; ok {
		apiAccel = v
	}

	// Derive variant when not explicitly overridden.
	apiVariant := variant
	if apiVariant == "" {
		switch accelUpper {
		case "T4", "L4", "A100", "A100-80GB", "H100", "G4":
			apiVariant = "GPU"
		case "V5E1", "V6E1":
			apiVariant = "TPU"
		default:
			apiVariant = "DEFAULT"
		}
	}

	return fmt.Sprintf("%s%s/assign?nbh=%s&authuser=0&variant=%s&accelerator=%s",
		baseURL, tunEndpoint,
		url.QueryEscape(notebookHash),
		url.QueryEscape(apiVariant),
		url.QueryEscape(apiAccel),
	)
}

// issueRequest performs an HTTP request with the standard Colab headers and
// strips the XSSI prefix from the response body.
func (c *Client) issueRequest(ctx context.Context, method, endpoint string, extraHeaders map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(raw))
	}

	// Strip the XSSI prefix that Google APIs prepend to JSON responses.
	body := strings.TrimPrefix(string(raw), xssiPrefix)
	return []byte(body), nil
}
