package api

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
	"unicode"

	"github.com/ysmaoui/jkit/internal/jenkins"
)

var scmBranchPath = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*(/[A-Za-z0-9_][A-Za-z0-9_.-]*)*$`)

type scmRemote struct {
	URL         string `xml:"url"`
	Credentials string `xml:"credentialsId"`
}

type pipelineSCM struct {
	XMLName    xml.Name `xml:"flow-definition"`
	Disabled   bool     `xml:"disabled"`
	Definition struct {
		Class string `xml:"class,attr"`
		Git   struct {
			Class    string      `xml:"class,attr"`
			Remotes  []scmRemote `xml:"userRemoteConfigs>hudson.plugins.git.UserRemoteConfig"`
			Branches []string    `xml:"branches>hudson.plugins.git.BranchSpec>name"`
		} `xml:"scm"`
		Script      string `xml:"scriptPath"`
		Lightweight bool   `xml:"lightweight"`
	} `xml:"definition"`
}

// scmRoot is the repository and credential used for pipelines under one top-level folder.
type scmRoot struct {
	URL         string `json:"url"`
	Credentials string `json:"credentials"`
}

// scmRoots reads JKIT_SCM_ROOTS, for example
// {"TEAM":{"url":"https://git.example.com/team/ci.git","credentials":"team-ci"}}.
func scmRoots() map[string]scmRoot {
	var roots map[string]scmRoot
	if json.Unmarshal([]byte(os.Getenv("JKIT_SCM_ROOTS")), &roots) != nil {
		return nil
	}
	return roots
}

func (c *Client) CreateSCMJob(ctx context.Context, job, branch, script string) (*jenkins.SCMJobReceipt, error) {
	top, _, _ := strings.Cut(job, "/")
	i := strings.LastIndexByte(job, '/')
	root, ok := scmRoots()[top]
	if err := ValidateConfigJob(job); err != nil || i < 0 || !ok || root.URL == "" || root.Credentials == "" {
		return nil, controlError("expected a job inside a folder configured in JKIT_SCM_ROOTS")
	}
	if !scmBranchPath.MatchString(branch) || len(branch) > 255 || strings.Contains(branch, "..") ||
		strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") || strings.Contains(branch, ".lock/") ||
		!fs.ValidPath(script) || script == "." || len(script) > 1024 || strings.ContainsAny(script, `\:`) || strings.ContainsFunc(script, unicode.IsControl) {
		return nil, controlError("expected a literal Git branch and repository-relative script path")
	}
	var wanted pipelineSCM
	wanted.XMLName.Local = "flow-definition"
	wanted.Definition.Class = "org.jenkinsci.plugins.workflow.cps.CpsScmFlowDefinition"
	git := &wanted.Definition.Git
	git.Class = "hudson.plugins.git.GitSCM"
	git.Remotes = []scmRemote{{root.URL, root.Credentials}}
	git.Branches = []string{"refs/heads/" + branch}
	wanted.Definition.Script = script
	wanted.Definition.Lightweight = true
	body, err := xml.Marshal(wanted)
	if err != nil {
		return nil, err
	}
	scoped, err := c.configClient()
	if err != nil {
		return nil, err
	}
	receipt := &jenkins.SCMJobReceipt{Job: job}
	parent := job[:i]
	data, _, err := scoped.controlRead(ctx, NormalizeJobPath(parent)+"/api/json?tree=fullName,_class")
	var folder struct {
		FullName string
		Class    string `json:"_class"`
	}
	if err != nil || json.Unmarshal(data, &folder) != nil || folder.FullName != parent || folder.Class != "com.cloudbees.hudson.plugins.folder.Folder" {
		receipt.State = "parent_unavailable"
		return receipt, nil
	}
	status, err := scoped.controlJob(ctx, job)
	if status != http.StatusNotFound {
		receipt.State = "destination_unavailable"
		if err == nil {
			receipt.State = "destination_exists"
		}
		return receipt, nil
	}
	path := NormalizeJobPath(parent) + "/createItem?" + url.Values{"name": {job[i+1:]}}.Encode()
	resp, err := scoped.postOnceResponseContext(ctx, path, bytes.NewReader(body), "application/xml")
	if err != nil {
		return nil, controlError("creation acknowledgement unavailable")
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		receipt.State = "creation_rejected"
		return receipt, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, controlError("creation acknowledgement unavailable")
	}
	if _, err := scoped.controlJob(ctx, job); err != nil {
		return nil, err
	}
	data, err = scoped.GetJobConfig(ctx, job)
	if err != nil {
		return nil, err
	}
	var actual pipelineSCM
	if ValidateCopyXML(data) != nil || configXMLDecoder(data).Decode(&actual) != nil || !reflect.DeepEqual(actual, wanted) {
		return nil, controlError("created job configuration could not be verified")
	}
	receipt.State = "verified"
	return receipt, nil
}
