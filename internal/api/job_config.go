package api

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const MaxJobConfigBytes = 1 << 20

// ValidateConfigJob accepts natural job paths, without URL or traversal syntax.
func ValidateConfigJob(job string) error {
	if len(job) > 1024 || !fs.ValidPath(job) || job == "." || strings.ContainsAny(job, "\\%?#") || strings.ContainsFunc(job, unicode.IsControl) {
		return fmt.Errorf("job configuration requires a canonical job path")
	}
	return nil
}

// ValidateConfigXML checks a copy-free token stream without rewriting the bytes.
// No DTD, custom entity declarations, external entities, or non-UTF8 encodings.
func ValidateConfigXML(data []byte) error {
	invalid := fmt.Errorf("job configuration must be nonempty UTF-8 XML without DTD or custom entities, at most 1 MiB")
	if len(data) == 0 || len(data) > MaxJobConfigBytes || !utf8.Valid(data) {
		return invalid
	}
	decoder := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})))
	depth, roots, tokenIndex := 0, 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return invalid
		}
		switch value := token.(type) {
		case xml.ProcInst:
			if strings.EqualFold(value.Target, "xml") && (value.Target != "xml" || tokenIndex != 0) {
				return invalid
			}
		case xml.Directive:
			return invalid
		case xml.StartElement:
			if depth == 0 {
				roots++
			}
			depth++
		case xml.EndElement:
			depth--
		case xml.CharData:
			if depth == 0 && len(bytes.TrimSpace(value)) != 0 {
				return invalid
			}
		}
		tokenIndex++
	}
	if roots != 1 || depth != 0 {
		return invalid
	}
	return nil
}

// configClient isolates strict redirect and logging policy from legacy callers.
func (c *Client) configClient() (*Client, error) {
	base, err := url.Parse(c.host)
	if err != nil || originForURL(base).host == "" || base.RawQuery != "" || base.ForceQuery || base.Fragment != "" || strings.Contains(c.host, "#") || base.RawPath != "" {
		return nil, fmt.Errorf("invalid configured Jenkins URL")
	}
	if base.Path != "" {
		if err := ValidateConfigJob(strings.TrimPrefix(base.Path, "/")); err != nil {
			return nil, fmt.Errorf("invalid configured Jenkins context path")
		}
	}
	scoped := *c
	client := *c.httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	// Sensitive configuration commands never emit transport error text.
	if verbose, ok := client.Transport.(*verboseTransport); ok {
		client.Transport = verbose.base
	}
	scoped.httpClient = &client
	scoped.crumbs = newCrumbIssuer(&scoped)
	scoped.crumbs.maxBytes = MaxJobConfigBytes
	return &scoped, nil
}

func (c *Client) GetJobConfig(ctx context.Context, job string) ([]byte, error) {
	if err := ValidateConfigJob(job); err != nil {
		return nil, err
	}
	scoped, err := c.configClient()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scoped.host+NormalizeJobPath(job)+"/config.xml", nil)
	if err != nil {
		return nil, fmt.Errorf("cannot create job configuration read")
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := scoped.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("job configuration read failed")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("job configuration read failed (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxJobConfigBytes+1))
	if err != nil {
		return nil, fmt.Errorf("job configuration read was incomplete")
	}
	if err := ValidateConfigXML(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (c *Client) UpdateJobConfig(ctx context.Context, job string, data []byte) error {
	if err := ValidateConfigJob(job); err != nil {
		return err
	}
	if err := ValidateConfigXML(data); err != nil {
		return err
	}
	scoped, err := c.configClient()
	if err != nil {
		return err
	}
	resp, err := scoped.PostOnceContext(ctx, NormalizeJobPath(job)+"/config.xml", bytes.NewReader(data), "application/xml")
	// Generic HTTP errors can contain echoed XML. Never return their body/cause.
	if err != nil {
		return fmt.Errorf("job configuration update was not acknowledged; verify its state before another change")
	}
	_ = resp.Body.Close()
	return nil
}
