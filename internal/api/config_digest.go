package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"sort"

	"github.com/ysmaoui/jkit/internal/jenkins"
)

// ConfigurationSHA256 hashes XML meaning rather than Jenkins' serialization.
// Leaf and mixed-content text stays exact; only structural indentation is dropped.
func ConfigurationSHA256(data []byte) (string, error) {
	if err := ValidateConfigXML(data); err != nil {
		return "", err
	}
	decoder := configXMLDecoder(data)
	raw := bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	type part struct {
		token  xml.Token
		parent int
		cdata  bool
	}
	var parts []part
	var stack []int
	children, text := map[int]bool{}, map[int]bool{}
	for {
		before := decoder.InputOffset()
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", controlError("cannot digest job configuration")
		}
		parent := -1
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		cdata := bytes.Contains(raw[before:decoder.InputOffset()], []byte("<![CDATA["))
		switch value := token.(type) {
		case xml.StartElement:
			if parent >= 0 {
				children[parent] = true
			}
			stack = append(stack, len(parts))
			sort.Slice(value.Attr, func(i, j int) bool {
				a, b := value.Attr[i], value.Attr[j]
				if a.Name.Space != b.Name.Space {
					return a.Name.Space < b.Name.Space
				}
				if a.Name.Local != b.Name.Local {
					return a.Name.Local < b.Name.Local
				}
				return a.Value < b.Value
			})
			token = value
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(bytes.TrimSpace(value)) > 0 || cdata {
				text[parent] = true
			}
		}
		parts = append(parts, part{xml.CopyToken(token), parent, cdata})
	}
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	var characters []byte
	flush := func() {
		if len(characters) > 0 {
			_ = encoder.Encode([]any{"text", string(characters)})
			characters = nil
		}
	}
	for _, part := range parts {
		if value, ok := part.token.(xml.CharData); ok {
			if !part.cdata && len(bytes.TrimSpace(value)) == 0 && (part.parent < 0 || children[part.parent] && !text[part.parent]) {
				continue
			}
			characters = append(characters, value...)
			continue
		}
		if value, ok := part.token.(xml.ProcInst); ok && value.Target == "xml" {
			continue
		}
		flush()
		switch value := part.token.(type) {
		case xml.StartElement:
			_ = encoder.Encode([]any{"start", value.Name, value.Attr})
		case xml.EndElement:
			_ = encoder.Encode([]any{"end", value.Name})
		case xml.Comment:
			_ = encoder.Encode([]any{"comment", string(value)})
		case xml.ProcInst:
			_ = encoder.Encode([]any{"instruction", value.Target, string(value.Inst)})
		}
	}
	flush()
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (c *Client) DigestJobConfig(ctx context.Context, job string) (*jenkins.JobConfigDigest, error) {
	data, err := c.GetJobConfig(ctx, job)
	if err != nil {
		return nil, err
	}
	configuration, err := ConfigurationSHA256(data)
	if err != nil {
		return nil, err
	}
	return &jenkins.JobConfigDigest{Job: job, SHA256: fmt.Sprintf("%x", sha256.Sum256(data)), ConfigurationSHA256: configuration}, nil
}
