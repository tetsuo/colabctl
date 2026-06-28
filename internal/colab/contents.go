package colab

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// FileContent is a file downloaded from a runtime via the Jupyter Contents API.
type FileContent struct {
	Name string
	Path string
	Data []byte
}

// FetchFile downloads a single file from the runtime identified by info, using
// the Jupyter Contents API over the runtime proxy. remotePath is interpreted
// relative to the Jupyter server root (an absolute path like /content/x works
// too). Directories are rejected with a hint to archive them first.
func (c *Client) FetchFile(ctx context.Context, info *AssignmentInfo, remotePath string) (*FileContent, error) {
	base := info.JupyterBaseURL()
	token := info.RuntimeProxyInfo.Token

	apiURL := fmt.Sprintf("%s/api/contents/%s?content=1&token=%s",
		base, escapeContentsPath(remotePath), url.QueryEscape(token))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")
	req.Header.Set("X-Colab-Runtime-Proxy-Token", token)

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return nil, fmt.Errorf("remote path %q not found on the runtime", remotePath)
		}
		return nil, fmt.Errorf("contents API HTTP %d: %s", resp.StatusCode, truncateForError(body))
	}

	var cr struct {
		Name    string          `json:"name"`
		Path    string          `json:"path"`
		Type    string          `json:"type"`
		Format  string          `json:"format"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body, &cr); err != nil {
		return nil, fmt.Errorf("parse contents response: %w", err)
	}

	if cr.Type == "directory" {
		return nil, fmt.Errorf("%q is a directory — archive it on the runtime first, "+
			"e.g. run `!tar czf /tmp/out.tgz -C %s .` then pull /tmp/out.tgz", remotePath, remotePath)
	}

	var data []byte
	switch cr.Format {
	case "base64":
		var s string
		if err := json.Unmarshal(cr.Content, &s); err != nil {
			return nil, err
		}
		// Jupyter may wrap the base64 payload across lines.
		s = strings.ReplaceAll(s, "\n", "")
		data, err = base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, fmt.Errorf("decode base64 content: %w", err)
		}
	case "text":
		var s string
		if err := json.Unmarshal(cr.Content, &s); err != nil {
			return nil, err
		}
		data = []byte(s)
	case "json":
		// Notebooks come back as a JSON object rather than a string.
		data = []byte(cr.Content)
	default:
		return nil, fmt.Errorf("unsupported content format %q for %q", cr.Format, remotePath)
	}

	return &FileContent{Name: cr.Name, Path: cr.Path, Data: data}, nil
}

// PutFile uploads data to remotePath on the runtime identified by info via the
// Jupyter Contents API. The parent directory must already exist.
func (c *Client) PutFile(ctx context.Context, info *AssignmentInfo, remotePath string, data []byte) error {
	base := info.JupyterBaseURL()
	token := info.RuntimeProxyInfo.Token

	apiURL := fmt.Sprintf("%s/api/contents/%s?token=%s",
		base, escapeContentsPath(remotePath), url.QueryEscape(token))

	payload, err := json.Marshal(map[string]interface{}{
		"type":    "file",
		"format":  "base64",
		"content": base64.StdEncoding.EncodeToString(data),
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, apiURL, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")
	req.Header.Set("X-Colab-Runtime-Proxy-Token", token)

	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("contents API HTTP %d: %s", resp.StatusCode, truncateForError(body))
	}
	return nil
}

// escapeContentsPath URL-escapes each path segment while keeping the slashes
// that separate them, and drops any leading slash the API does not expect.
func escapeContentsPath(p string) string {
	p = strings.TrimPrefix(p, "/")
	parts := strings.Split(p, "/")
	for i, s := range parts {
		parts[i] = url.PathEscape(s)
	}
	return strings.Join(parts, "/")
}

func truncateForError(b []byte) string {
	const max = 300
	if len(b) > max {
		return string(b[:max])
	}
	return string(b)
}
