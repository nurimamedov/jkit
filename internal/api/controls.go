package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/ysmaoui/jkit/internal/jenkins"
)

func controlError(message string) error { return &jenkins.ControlError{Message: message} }

// controlRead makes one bounded, unredirected read without exposing response bodies.
func (c *Client) controlRead(ctx context.Context, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.host+path, nil)
	if err != nil {
		return nil, 0, controlError("cannot create control read")
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, 0, controlError("control read failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, resp.StatusCode, controlError("control read did not return HTTP 200")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxJobConfigBytes+1))
	if err != nil || len(data) > MaxJobConfigBytes {
		return nil, resp.StatusCode, controlError("control read was incomplete or too large")
	}
	return data, resp.StatusCode, nil
}

func (c *Client) controlJob(ctx context.Context, job string) (int, error) {
	data, status, err := c.controlRead(ctx, NormalizeJobPath(job)+"/api/json?tree=fullName")
	if err != nil {
		return status, err
	}
	var identity struct {
		FullName string `json:"fullName"`
	}
	if json.Unmarshal(data, &identity) != nil || identity.FullName != job {
		return status, controlError("job identity did not match the exact target")
	}
	return status, nil
}

// AbortBuildOnce addresses only a numbered build and acknowledges submission,
// not completion. A failed acknowledgement must never be automatically retried.
func (c *Client) AbortBuildOnce(ctx context.Context, job string, number int) (*jenkins.AbortReceipt, error) {
	if err := ValidateConfigJob(job); err != nil {
		return nil, err
	}
	if number <= 0 {
		return nil, controlError("abort requires a positive explicit build number")
	}
	scoped, err := c.configClient()
	if err != nil {
		return nil, err
	}
	if _, err := scoped.controlJob(ctx, job); err != nil {
		return nil, err
	}
	path := fmt.Sprintf("%s/%d", NormalizeJobPath(job), number)
	data, _, err := scoped.controlRead(ctx, path+"/api/json?tree=number,building,url,result")
	if err != nil {
		return nil, err
	}
	var build struct {
		Number   int    `json:"number"`
		Building *bool  `json:"building"`
		URL      string `json:"url"`
		Result   string `json:"result"`
	}
	if json.Unmarshal(data, &build) != nil || build.Number != number || build.Building == nil || build.URL != scoped.host+path+"/" {
		return nil, controlError("build identity did not match the exact target")
	}
	receipt := &jenkins.AbortReceipt{Job: job, BuildNumber: number, AlreadyCompleted: true}
	if !*build.Building {
		if build.Result == "" {
			return nil, controlError("build has no terminal result; read its exact state before another change")
		}
		return receipt, nil
	}
	resp, err := scoped.postOnceResponseContext(ctx, path+"/stop", nil, "")
	if err != nil {
		return nil, controlError("abort was not acknowledged; read the exact build state before another change")
	}

	_ = resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if resp.StatusCode != http.StatusFound || !scoped.exactBuildLocation(resp.Header.Get("Location"), path+"/") {
			return nil, controlError("abort was not acknowledged; read the exact build state before another change")
		}
	}
	receipt.Submitted, receipt.AlreadyCompleted = true, false
	return receipt, nil
}

// ValidateCopyXML allows standalone build jobs without persisted automatic triggers.
func ValidateCopyXML(data []byte) error {
	if err := ValidateConfigXML(data); err != nil {
		return err
	}
	decoder := configXMLDecoder(data)
	depth, triggerDepth := 0, 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return controlError("unsafe source job configuration")
		}
		switch value := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 && (value.Name.Space != "" || value.Name.Local != "project" && value.Name.Local != "flow-definition") {
				return controlError("copy supports only standalone freestyle or pipeline jobs")
			}
			lower := strings.ToLower(value.Name.Local)

			for _, attribute := range value.Attr {
				lowerValue := strings.ToLower(attribute.Value)
				if attribute.Name.Local == "class" && (strings.Contains(lowerValue, "trigger") || strings.Contains(lowerValue, "branchjobproperty")) {
					return controlError("copy refuses automatic triggers or managed branch jobs")
				}
			}
			if triggerDepth != 0 || strings.HasSuffix(lower, "trigger") || strings.Contains(lower, "branchjobproperty") {
				return controlError("copy refuses automatic triggers or managed branch jobs")
			}
			if lower == "triggers" {
				triggerDepth = depth
			}
		case xml.CharData:
			if triggerDepth != 0 && len(bytes.TrimSpace(value)) != 0 {
				return controlError("copy refuses automatic triggers")
			}
		case xml.EndElement:
			if depth == triggerDepth {
				triggerDepth = 0
			}
			depth--
		}
	}
}

// CopyJobConfig creates a previously absent job once, then verifies its exact
// identity and effective XML configuration. Even uncertain responses never cause a second mutation.
func (c *Client) CopyJobConfig(ctx context.Context, source, destination, expectedSHA256 string) (*jenkins.CopyJobReceipt, error) {
	for _, job := range []string{source, destination} {
		if err := ValidateConfigJob(job); err != nil {
			return nil, err
		}
	}
	expected, err := hex.DecodeString(expectedSHA256)
	if err != nil || len(expected) != sha256.Size || strings.ToLower(expectedSHA256) != expectedSHA256 {
		return nil, controlError("copy requires a lowercase SHA-256 source precondition")
	}
	if source == destination {
		return nil, controlError("copy destination must differ from source")
	}
	scoped, err := c.configClient()
	if err != nil {
		return nil, err
	}
	if _, err := scoped.controlJob(ctx, source); err != nil {
		return nil, err
	}
	data, err := scoped.GetJobConfig(ctx, source)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(data)
	if !bytes.Equal(hash[:], expected) {
		return nil, controlError("source configuration changed since approval")
	}
	if err := ValidateCopyXML(data); err != nil {
		return nil, err
	}
	if status, err := scoped.controlJob(ctx, destination); status != http.StatusNotFound {
		if err != nil {
			return nil, err
		}
		return nil, controlError("copy refuses an existing destination")
	}
	parent, name := "", destination
	if i := strings.LastIndexByte(destination, '/'); i >= 0 {
		parent, name = destination[:i], destination[i+1:]
		if _, err := scoped.controlJob(ctx, parent); err != nil {
			return nil, err
		}
	}
	path := NormalizeJobPath(parent) + "/createItem?" + url.Values{"name": {name}}.Encode()
	resp, err := scoped.PostOnceContext(ctx, path, bytes.NewReader(data), "application/xml")
	if err != nil {
		return nil, controlError("job copy was not acknowledged; read the exact destination before another change")
	}
	_ = resp.Body.Close()
	if _, err := scoped.controlJob(ctx, destination); err != nil {
		return nil, controlError("job copy was accepted but destination identity could not be verified; do not repeat it")
	}

	actual, err := scoped.DigestJobConfig(ctx, destination)
	configuration, digestErr := ConfigurationSHA256(data)
	if err != nil || digestErr != nil || actual.ConfigurationSHA256 != configuration {
		return nil, controlError("job copy was accepted but destination configuration could not be verified; do not repeat it")
	}
	return &jenkins.CopyJobReceipt{Source: source, Destination: destination, SubmittedSHA256: expectedSHA256, ConfigurationSHA256: configuration, DestinationSHA256: actual.SHA256, Accepted: true, Verified: true}, nil
}

// Jenkins' stop endpoint redirects to the same run; this validates its receipt
// without following it or sending a second request.
func (c *Client) exactBuildLocation(location, path string) bool {
	base, err := url.Parse(c.host)
	if err != nil {
		return false
	}
	loc, err := url.Parse(location)
	if err != nil || location == "" || loc.User != nil || loc.Opaque != "" || loc.RawPath != "" || strings.ContainsAny(location, "?#") {
		return false
	}
	if loc.IsAbs() {
		if !originForURL(base).allows(loc) {
			return false
		}
	} else if loc.Host != "" || !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") {
		return false
	}
	return loc.Path == strings.TrimRight(base.Path, "/")+path
}
