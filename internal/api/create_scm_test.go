package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateSCMJobRequiresConfiguredRoot(t *testing.T) {
	t.Setenv("JKIT_SCM_ROOTS", `{"TEAM":{"url":"https://git.example.com/team/ci.git","credentials":"team-ci"}}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL.Path)
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "u", "t")
	for _, job := range []string{"OTHER/new", "TEAM", "../TEAM/new"} {
		_, err := client.CreateSCMJob(context.Background(), job, "main", "ci/build.groovy")
		require.Error(t, err, job)
	}
	t.Setenv("JKIT_SCM_ROOTS", "")
	_, err := client.CreateSCMJob(context.Background(), "TEAM/new", "main", "ci/build.groovy")
	require.Error(t, err)
}

func TestCreateSCMJobUsesConfiguredRepository(t *testing.T) {
	t.Setenv("JKIT_SCM_ROOTS", `{"TEAM":{"url":"https://git.example.com/team/ci.git","credentials":"team-ci"}}`)
	var created []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/job/TEAM/api/json":
			io.WriteString(w, `{"fullName":"TEAM","_class":"com.cloudbees.hudson.plugins.folder.Folder"}`)
		case "/job/TEAM/job/new/api/json":
			if created == nil {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, `{"fullName":"TEAM/new"}`)
		case "/crumbIssuer/api/json":
			w.WriteHeader(404)
		case "/job/TEAM/createItem":
			assert.Equal(t, "new", r.URL.Query().Get("name"))
			created, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
		case "/job/TEAM/job/new/config.xml":
			w.Write(created)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	receipt, err := NewClient(srv.URL, "u", "t").CreateSCMJob(context.Background(), "TEAM/new", "main", "ci/build.groovy")
	require.NoError(t, err)
	assert.Equal(t, "verified", receipt.State)
	body := string(created)
	assert.True(t, strings.Contains(body, "<url>https://git.example.com/team/ci.git</url>"), body)
	assert.True(t, strings.Contains(body, "<credentialsId>team-ci</credentialsId>"), body)
	assert.True(t, strings.Contains(body, "<scriptPath>ci/build.groovy</scriptPath>"), body)
}
