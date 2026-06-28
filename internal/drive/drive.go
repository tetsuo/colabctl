// Package drive provides a thin wrapper around the Google Drive API v3 for
// operations relevant to Colab notebooks.
package drive

import (
	"context"
	"fmt"
	"net/http"

	gdrive "google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

const colabMIMEType = "application/vnd.google.colaboratory"

// Notebook is a minimal representation of a Colab notebook file in Drive.
type Notebook struct {
	ID       string
	Name     string
	Modified string // RFC 3339 string
	URL      string
}

// Client wraps the Drive API service.
type Client struct {
	svc *gdrive.Service
}

// New creates a Drive client using the provided authenticated HTTP client.
func New(ctx context.Context, httpClient *http.Client) (*Client, error) {
	svc, err := gdrive.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("create drive service: %w", err)
	}
	return &Client{svc: svc}, nil
}

// ListNotebooks returns all Colab notebooks (.ipynb files with the Colab MIME
// type) that are owned by or shared with the authenticated user.
func (c *Client) ListNotebooks(ctx context.Context) ([]*Notebook, error) {
	var notebooks []*Notebook
	pageToken := ""
	fields := googleapi.Field("nextPageToken, files(id, name, modifiedTime, webViewLink)")
	query := fmt.Sprintf("mimeType='%s' and trashed=false", colabMIMEType)

	for {
		call := c.svc.Files.List().
			Q(query).
			Fields(fields).
			OrderBy("modifiedTime desc").
			PageSize(100).
			Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			return nil, fmt.Errorf("list drive files: %w", err)
		}
		for _, f := range resp.Files {
			notebooks = append(notebooks, &Notebook{
				ID:       f.Id,
				Name:     f.Name,
				Modified: f.ModifiedTime,
				URL:      f.WebViewLink,
			})
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	return notebooks, nil
}

// GetNotebook fetches the raw JSON bytes of a notebook from Drive by its file
// ID. The content is the raw .ipynb JSON.
func (c *Client) GetNotebook(ctx context.Context, fileID string) ([]byte, error) {
	resp, err := c.svc.Files.Export(fileID, "application/json").Context(ctx).Download()
	if err != nil {
		// Colab files are not Google Docs, so Export will not work.
		// Use the plain media download instead.
		resp2, err2 := c.svc.Files.Get(fileID).Context(ctx).Download()
		if err2 != nil {
			return nil, fmt.Errorf("download notebook (file %s): %w", fileID, err)
		}
		defer resp2.Body.Close()
		buf := make([]byte, 0, 512*1024)
		tmp := make([]byte, 32*1024)
		for {
			n, readErr := resp2.Body.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
			}
			if readErr != nil {
				break
			}
		}
		return buf, nil
	}
	defer resp.Body.Close()
	buf := make([]byte, 0, 512*1024)
	tmp := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if readErr != nil {
			break
		}
	}
	return buf, nil
}
