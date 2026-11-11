package api

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAbortExactReceipt(t *testing.T) {
	for _, mode := range []string{"ok", "redirect", "completed", "wrong-job", "wrong-build", "wrong-url", "missing-building", "no-terminal", "bad-redirect", "foreign-redirect", "403", "503", "lost"} {
		t.Run(mode, func(t *testing.T) {
			posts, follows := 0, 0
			var host string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/jenkins/job/team/job/demo/api/json":
					assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
					name := "team/demo"
					if mode == "wrong-job" {
						name = "other"
					}
					fmt.Fprintf(w, `{"fullName":%q}`, name)
				case "/jenkins/job/team/job/demo/42/api/json":
					n := 42
					if mode == "wrong-build" {
						n = 43
					}
					buildURL := host + "/job/team/job/demo/42/"
					if mode == "wrong-url" {
						buildURL = host + "/job/other/42/"
					}
					if mode == "missing-building" {
						fmt.Fprintf(w, `{"number":42,"url":%q}`, buildURL)
						return
					}

					if mode == "no-terminal" {
						fmt.Fprintf(w, `{"number":42,"building":false,"url":%q}`, buildURL)
						return
					}
					fmt.Fprintf(w, `{"number":%d,"building":%v,"url":%q,"result":"SUCCESS"}`, n, mode != "completed", buildURL)
				case "/jenkins/crumbIssuer/api/json":
					w.WriteHeader(404)
				case "/jenkins/job/team/job/demo/42/stop":
					require.Equal(t, "POST", r.Method)
					posts++
					switch mode {
					case "redirect":
						w.Header().Set("Location", "/jenkins/job/team/job/demo/42/")
						w.WriteHeader(302)
					case "bad-redirect":
						w.Header().Set("Location", "/jenkins/job/other/42/")
						w.WriteHeader(302)
					case "foreign-redirect":
						w.Header().Set("Location", "https://other.invalid/jenkins/job/team/job/demo/42/")
						w.WriteHeader(302)
					case "403":
						w.WriteHeader(403)
					case "503":
						w.WriteHeader(503)
					case "lost":
						conn, _, err := w.(http.Hijacker).Hijack()
						require.NoError(t, err)
						conn.Close()
					default:
						w.WriteHeader(200)
					}
				default:
					follows++
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			host = srv.URL + "/jenkins"
			receipt, err := NewClient(host, "u", "t").AbortBuildOnce(context.Background(), "team/demo", 42)
			switch mode {
			case "ok", "redirect":
				require.NoError(t, err)
				assert.True(t, receipt.Submitted)
				assert.False(t, receipt.AlreadyCompleted)
				assert.Equal(t, 42, receipt.BuildNumber)
			case "completed":
				require.NoError(t, err)
				assert.False(t, receipt.Submitted)
				assert.True(t, receipt.AlreadyCompleted)
			default:
				require.Error(t, err)
				assert.Nil(t, receipt)
			}
			if strings.HasPrefix(mode, "wrong-") || mode == "missing-building" || mode == "no-terminal" || mode == "completed" {
				assert.Zero(t, posts)
			} else {
				assert.Equal(t, 1, posts)
			}
			assert.Zero(t, follows)
		})
	}
}

func TestCopyJobExactOnce(t *testing.T) {
	for _, mode := range []string{"ok", "existing", "hash", "source-identity", "destination-identity", "source-triggers", "readback", "readback-error", "reserialized", "redirect", "403", "503", "lost"} {
		t.Run(mode, func(t *testing.T) {
			posts, follows := 0, 0
			data := []byte(configFixture)
			if mode == "source-triggers" {
				data = []byte(`<project><triggers><hudson.triggers.TimerTrigger><spec>* * * * *</spec></hudson.triggers.TimerTrigger></triggers></project>`)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(data))
			if mode == "hash" {
				hash = strings.Repeat("0", 64)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && !strings.Contains(r.URL.Path, "crumbIssuer") {
					assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
				}
				switch r.URL.Path {
				case "/jenkins/job/team/job/source/api/json":
					if mode == "source-identity" {
						io.WriteString(w, `{"fullName":"other"}`)
					} else {
						io.WriteString(w, `{"fullName":"team/source"}`)
					}
				case "/jenkins/job/team/job/source/config.xml":
					w.Write(data)
				case "/jenkins/job/target/api/json":
					io.WriteString(w, `{"fullName":"target"}`)
				case "/jenkins/job/target/job/dest/api/json":
					if posts == 0 && mode != "existing" {
						w.WriteHeader(404)
						return
					}
					if mode == "destination-identity" {
						io.WriteString(w, `{"fullName":"wrong"}`)
					} else {
						io.WriteString(w, `{"fullName":"target/dest"}`)
					}
				case "/jenkins/job/target/job/dest/config.xml":
					if mode == "reserialized" {
						io.WriteString(w, `<project><description>private-fixture &amp; exact</description></project>`)
						return
					}
					if mode == "readback" {
						io.WriteString(w, `<project/>`)
						return
					}
					if mode == "readback-error" {
						w.WriteHeader(500)
						io.WriteString(w, "private-secret")
						return
					}
					w.Write(data)
				case "/jenkins/crumbIssuer/api/json":
					w.WriteHeader(404)
				case "/jenkins/job/target/createItem":
					posts++
					assert.Equal(t, "POST", r.Method)
					assert.Equal(t, "dest", r.URL.Query().Get("name"))
					assert.Equal(t, "application/xml", r.Header.Get("Content-Type"))
					got, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					assert.Equal(t, data, got)
					switch mode {
					case "redirect":
						w.Header().Set("Location", "/replayed")
						w.WriteHeader(307)
					case "403":
						w.WriteHeader(403)
					case "503":
						w.WriteHeader(503)
					case "lost":
						conn, _, err := w.(http.Hijacker).Hijack()
						require.NoError(t, err)
						conn.Close()
					default:
						w.WriteHeader(200)
					}
				default:
					follows++
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			receipt, err := NewClient(srv.URL+"/jenkins", "u", "t").CopyJobConfig(context.Background(), "team/source", "target/dest", hash)
			if mode == "ok" || mode == "reserialized" {
				require.NoError(t, err)
				assert.True(t, receipt.Accepted)
				assert.True(t, receipt.Verified)
				assert.Equal(t, hash, receipt.SubmittedSHA256)
			} else {
				require.Error(t, err)
				assert.Nil(t, receipt)
				assert.NotContains(t, err.Error(), "private-secret")
			}
			switch mode {
			case "existing", "hash", "source-identity", "source-triggers":
				assert.Zero(t, posts)
			default:
				assert.Equal(t, 1, posts)
			}
			assert.Zero(t, follows)
		})
	}
}

func TestCopyXMLSafety(t *testing.T) {
	for _, data := range []string{`<project><triggers/></project>`, `<flow-definition><properties><org.jenkinsci.plugins.workflow.job.properties.PipelineTriggersJobProperty><triggers/></org.jenkinsci.plugins.workflow.job.properties.PipelineTriggersJobProperty></properties></flow-definition>`} {
		assert.NoError(t, ValidateCopyXML([]byte(data)))
	}
	for _, data := range []string{`<com.cloudbees.hudson.plugins.folder.Folder/>`, `<project><triggers>secret</triggers></project>`, `<flow-definition><properties><jenkins.branch.BranchJobProperty/></properties></flow-definition>`, `<!DOCTYPE project><project/>`, `<project><properties><SCMTrigger/></properties></project>`, `<project><triggers><empty/></triggers></project>`, `<project><properties><property class="hudson.triggers.TimerTrigger"/></properties></project>`} {
		assert.Error(t, ValidateCopyXML([]byte(data)))
	}
}
