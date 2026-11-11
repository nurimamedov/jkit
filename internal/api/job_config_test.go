package api

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const configFixture = "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<project><description>private-fixture &amp; exact</description></project>\n"

func TestJobConfigExactBytes(t *testing.T) {
	var posted []byte
	var reads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jenkins/crumbIssuer/api/json" {
			w.WriteHeader(404)
			return
		}
		assert.Equal(t, "/jenkins/job/team/job/demo/config.xml", r.URL.Path)
		user, token, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "u", user)
		assert.Equal(t, "t", token)
		if r.Method == "POST" {
			var err error
			posted, err = io.ReadAll(r.Body)
			assert.NoError(t, err)
			assert.Equal(t, "application/xml", r.Header.Get("Content-Type"))
			w.WriteHeader(200)
			return
		}
		reads.Add(1)
		assert.Equal(t, "identity", r.Header.Get("Accept-Encoding"))
		_, _ = io.WriteString(w, configFixture)
	}))
	defer srv.Close()
	client := NewClient(srv.URL+"/jenkins", "u", "t")
	data, err := client.GetJobConfig(context.Background(), "team/demo")
	require.NoError(t, err)
	assert.Equal(t, []byte(configFixture), data)
	require.NoError(t, client.UpdateJobConfig(context.Background(), "team/demo", data))
	assert.Equal(t, data, posted)
	assert.Equal(t, int32(1), reads.Load())
}

func TestJobConfigRejectsInputBeforeNetwork(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(400) }))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "t")
	for _, data := range [][]byte{nil, []byte("  "), []byte("<project>"), []byte("<a/><b/>"), []byte("<a/><?xml version='1.0'?>"), []byte("secret<project/>"), []byte("<!DOCTYPE project><project/>"), []byte("<!DOCTYPE project [<!ENTITY leak SYSTEM 'file:///tmp/secret'>]><project>&leak;</project>"), []byte("<project>&unknown;</project>"), {0xff}, []byte("<?xml version='1.0' encoding='ISO-8859-1'?><project/>"), bytes.Repeat([]byte("x"), (1<<20)+1)} {
		err := c.UpdateJobConfig(context.Background(), "demo", data)
		assert.Error(t, err)
		if err != nil {
			assert.NotContains(t, err.Error(), "secret")
		}
	}
	for _, job := range []string{"", "/demo", "demo/", "a//b", ".", "a/../b", "a/%2e%2e/b", "a\\b", "https://other.invalid/x", "demo?x", "demo#x", "a\nb"} {
		_, err := c.GetJobConfig(context.Background(), job)
		assert.Error(t, err)
		assert.Error(t, c.UpdateJobConfig(context.Background(), job, []byte(configFixture)))
	}
	assert.Zero(t, calls.Load())
}

func TestJobConfigBoundsAndSafeErrors(t *testing.T) {
	for _, mode := range []string{"limit", "overflow", "broken", "html", "server-error"} {
		t.Run(mode, func(t *testing.T) {
			data := []byte("<project>" + strings.Repeat("x", (1<<20)-19) + "</project>")
			if mode == "overflow" {
				data = append(data, ' ')
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "server-error" {
					w.WriteHeader(500)
					_, _ = io.WriteString(w, "private-error-secret")
					return
				}
				if mode == "broken" {
					w.Header().Set("Content-Length", "9999999")
				}
				if mode == "html" {
					_, _ = io.WriteString(w, "private-error-secret")
					return
				}
				_, _ = w.Write(data)
			}))
			defer srv.Close()
			got, err := NewClient(srv.URL, "u", "t").GetJobConfig(context.Background(), "demo")
			if mode == "limit" {
				require.NoError(t, err)
				assert.Equal(t, data, got)
			} else {
				require.Error(t, err)
				assert.Nil(t, got)
				assert.NotContains(t, err.Error(), "private-error-secret")
			}
		})
	}
}

func TestJobConfigNoRedirectsOrMutationRetries(t *testing.T) {
	for _, phase := range []string{"get", "crumb", "post"} {
		for _, status := range []int{301, 302, 303, 307, 308, 403, 503} {
			t.Run(phase+http.StatusText(status), func(t *testing.T) {
				var calls, posts, redirects atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/replayed" {
						redirects.Add(1)
						w.WriteHeader(200)
						return
					}
					if r.Method == "POST" {
						posts.Add(1)
					}
					if r.URL.Path == "/crumbIssuer/api/json" && phase != "crumb" {
						w.WriteHeader(404)
						return
					}
					calls.Add(1)
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "private-error-secret")
				}))
				defer srv.Close()
				c := NewClient(srv.URL, "u", "t")
				var err error
				if phase == "get" {
					_, err = c.GetJobConfig(context.Background(), "demo")
				} else {
					err = c.UpdateJobConfig(context.Background(), "demo", []byte(configFixture))
				}
				require.Error(t, err)
				assert.NotContains(t, err.Error(), "private-error-secret")
				assert.Equal(t, int32(1), calls.Load())
				assert.Zero(t, redirects.Load())
				if phase == "post" {
					assert.Equal(t, int32(1), posts.Load())
				} else {
					assert.Zero(t, posts.Load())
				}
			})
		}
	}
}

func TestJobConfigDeadlineCoversCrumb(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
		}
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(404)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := NewClient(srv.URL, "u", "t").UpdateJobConfig(ctx, "demo", []byte(configFixture))
	assert.Error(t, err)
	assert.Less(t, time.Since(start), 90*time.Millisecond)
	assert.Zero(t, posts.Load())
}

func TestJobConfigPreservesTLSOriginAuthentication(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "user", u)
		assert.Equal(t, "token", p)
		_, _ = io.WriteString(w, configFixture)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "user", "token")
	c.httpClient.Transport.(*authTransport).base = srv.Client().Transport
	data, err := c.GetJobConfig(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, []byte(configFixture), data)
}

func TestJobConfigPreservesBOMCRLFAndUnicode(t *testing.T) {
	data := []byte("\xef\xbb\xbf<?xml version='1.0' encoding='UTF-8'?>\r\n<project><description>Проверка &amp; tekst</description></project>\r\n")
	var posted []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/crumbIssuer/api/json" {
			w.WriteHeader(404)
			return
		}
		if r.Method == "POST" {
			posted, _ = io.ReadAll(r.Body)
			w.WriteHeader(200)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "u", "t")
	got, err := c.GetJobConfig(context.Background(), "demo")
	require.NoError(t, err)
	assert.Equal(t, data, got)
	require.NoError(t, c.UpdateJobConfig(context.Background(), "demo", got))
	assert.Equal(t, data, posted)
}

func TestJobConfigRejectsOversizedCrumbBeforePost(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			return
		}
		_, _ = io.WriteString(w, `{"crumb":"`+strings.Repeat("x", 1<<20)+`","crumbRequestField":"Jenkins-Crumb"}`)
	}))
	defer srv.Close()
	err := NewClient(srv.URL, "u", "t").UpdateJobConfig(context.Background(), "demo", []byte(configFixture))
	require.Error(t, err)
	assert.Zero(t, posts.Load())
}

func TestJobConfigXML11ValidationPreservesExactBytes(t *testing.T) {
	data := []byte("\xef\xbb\xbf<?xml version='1.1' encoding='UTF-8'?>\r\n<project><description>version='1.1' &amp; Проверка</description><triggers/></project>\r\n")
	original := bytes.Clone(data)
	require.NoError(t, ValidateConfigXML(data))
	require.NoError(t, ValidateCopyXML(data))
	assert.Equal(t, original, data)
	for _, invalid := range []string{"<?xml version='1.1'?><project>&#x1b;</project>", "<?xml version='1.1'?><project>\x1b</project>", "<?xml version='1.1'?><!DOCTYPE project><project/>"} {
		assert.Error(t, ValidateConfigXML([]byte(invalid)))
	}
}
