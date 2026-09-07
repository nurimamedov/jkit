package api

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
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
