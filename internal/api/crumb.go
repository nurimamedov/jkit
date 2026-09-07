package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
)

type crumbInfo struct {
	Crumb             string `json:"crumb"`
	CrumbRequestField string `json:"crumbRequestField"`
}

type crumbIssuer struct {
	maxBytes int64
	mu       sync.Mutex
	client   *Client
	crumb    *crumbInfo
}

func newCrumbIssuer(c *Client) *crumbIssuer {
	return &crumbIssuer{client: c}
}

func (ci *crumbIssuer) ensureCrumb() (*crumbInfo, error) {
	return ci.ensureCrumbContext(context.Background())
}

func (ci *crumbIssuer) ensureCrumbContext(ctx context.Context) (*crumbInfo, error) {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	if ci.crumb != nil {
		return ci.crumb, nil
	}
	return ci.fetchCrumbContext(ctx)
}

func (ci *crumbIssuer) fetchCrumb() (*crumbInfo, error) {
	return ci.fetchCrumbContext(context.Background())
}

func (ci *crumbIssuer) fetchCrumbContext(ctx context.Context) (*crumbInfo, error) {
	u := ci.client.host + "/crumbIssuer/api/json"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := ci.client.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		// CSRF protection disabled on this Jenkins
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching crumb: HTTP %d", resp.StatusCode)
	}
	var reader io.Reader = resp.Body
	if ci.maxBytes > 0 {
		data, err := io.ReadAll(io.LimitReader(resp.Body, ci.maxBytes+1))
		if err != nil || int64(len(data)) > ci.maxBytes {
			return nil, fmt.Errorf("invalid bounded crumb response")
		}
		reader = bytes.NewReader(data)
	}
	var info crumbInfo
	if err := json.NewDecoder(reader).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding crumb: %w", err)
	}
	ci.crumb = &info
	return &info, nil
}

func (ci *crumbIssuer) invalidate() {
	ci.mu.Lock()
	defer ci.mu.Unlock()
	ci.crumb = nil
}
