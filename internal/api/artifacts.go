package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/ysmaoui/jkit/internal/jenkins"
)

func (c *Client) GetArtifacts(jobPath string, number int) ([]jenkins.Artifact, error) {
	path := fmt.Sprintf("%s/%d/api/json", NormalizeJobPath(jobPath), number)
	query := url.Values{"tree": {"artifacts[fileName,relativePath]"}}

	resp, err := c.Get(path, query)
	if err != nil {
		return nil, fmt.Errorf("getting artifacts: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Artifacts []jenkins.Artifact `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding artifacts: %w", err)
	}
	return result.Artifacts, nil
}

func (c *Client) DownloadArtifact(jobPath string, number int, relativePath string) (io.ReadCloser, error) {
	path := fmt.Sprintf("%s/%d/artifact/%s", NormalizeJobPath(jobPath), number, relativePath)

	resp, err := c.Get(path, nil)
	if err != nil {
		return nil, fmt.Errorf("downloading artifact: %w", err)
	}
	return resp.Body, nil
}

// ValidateArtifactPath rejects path and URL syntax that could leave the selected
// build's artifact directory. Percent escapes are not accepted as input.
func ValidateArtifactPath(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "\\%?#") || strings.ContainsFunc(name, unicode.IsControl) {
		return fmt.Errorf("artifact name must be a canonical relative path")
	}
	return nil
}

// ReadArtifactBounded returns an exact relativePath match, fully read before
// output. The fixed 64 KiB ceiling keeps machine evidence small and atomic.
func (c *Client) ReadArtifactBounded(jobPath string, number int, name string) ([]byte, error) {
	if number <= 0 {
		return nil, fmt.Errorf("an explicit positive build number is required")
	}
	if err := ValidateArtifactPath(name); err != nil {
		return nil, err
	}
	artifacts, err := c.GetArtifacts(jobPath, number)
	if err != nil {
		return nil, err
	}
	matches := 0
	for _, artifact := range artifacts {
		if artifact.RelativePath == name {
			matches++
		}
	}
	if matches != 1 {
		return nil, fmt.Errorf("artifact must have exactly one matching relativePath")
	}
	body, err := c.DownloadArtifact(jobPath, number, name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	const maxBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading artifact: %w", err)
	}
	if len(data) > maxBytes {
		return nil, fmt.Errorf("artifact exceeds 64 KiB limit")
	}
	return data, nil
}

const MaxArtifactFileBytes = 512 << 20

// OpenArtifactFile permits only Jenkins' dedicated static artifact host to serve a
// redirected file. The second client has no cookie jar or authentication transport.
func (c *Client) OpenArtifactFile(ctx context.Context, jobPath string, number int, name string) (io.ReadCloser, error) {
	if number <= 0 || !fs.ValidPath(name) || name == "." || strings.Contains(name, "\\") || strings.ContainsFunc(name, unicode.IsControl) {
		return nil, errors.New("an explicit build and canonical relative artifact path are required")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	link := c.host + fmt.Sprintf("%s/%d/artifact/%s", NormalizeJobPath(jobPath), number, strings.Join(parts, "/"))
	first := *c.httpClient
	first.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, err
	}
	resp, err := first.Do(req)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case 301, 302, 303, 307, 308:
		dest, locationErr := resp.Location()
		resp.Body.Close()
		static := httpOrigin{"https", "jenkins-static.example.com", 443}
		origin := httpOrigin{"https", "jenkins.example.com", 443}
		if locationErr != nil || !origin.allows(req.URL) || !static.allows(dest) {
			return nil, errOriginBoundary
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, dest.String(), nil)
		if err != nil {
			return nil, errors.New("invalid static artifact location")
		}
		anonymous := &http.Client{Timeout: first.Timeout, CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if !static.allows(next.URL) {
				return errOriginBoundary
			}
			if len(via) >= 10 {
				return errRedirectLimit
			}
			next.Header.Del("Referer")
			return nil
		}}
		resp, err = anonymous.Do(req)
		if err != nil {
			return nil, errors.New("static artifact download failed")
		}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("artifact download returned HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > MaxArtifactFileBytes {
		resp.Body.Close()
		return nil, &http.MaxBytesError{Limit: MaxArtifactFileBytes}
	}
	return http.MaxBytesReader(nil, resp.Body, MaxArtifactFileBytes), nil
}
