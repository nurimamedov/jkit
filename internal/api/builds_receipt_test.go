package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTriggerReceiptLocation(t *testing.T) {
	for _, tc := range []struct {
		name, location, prefix string
		valid                  bool
	}{
		{"absolute", "$ORIGIN/queue/item/42/", "", true},
		{"relative", "/queue/item/42/", "", true},
		{"context", "$ORIGIN/jenkins/queue/item/42/", "/jenkins", true},
		{"context relative", "/jenkins/queue/item/42/", "/jenkins", true},
		{"outside context", "$ORIGIN/queue/item/42/", "/jenkins", false},
		{"missing", "", "", false},
		{"foreign", "http://example.invalid/queue/item/42/", "", false},
		{"port", "http://127.0.0.1:1/queue/item/42/", "", false},
		{"userinfo", "http://user:secret@127.0.0.1/queue/item/42/", "", false},
		{"wrong path", "/job/demo/42/", "", false},
		{"zero", "/queue/item/0/", "", false},
		{"negative", "/queue/item/-42/", "", false},
		{"leading zero", "/queue/item/042/", "", false},
		{"plus", "/queue/item/+42/", "", false},
		{"overflow", "/queue/item/99999999999999999999999/", "", false},
		{"missing slash", "/queue/item/42", "", false},
		{"extra slash", "/queue/item/42//", "", false},
		{"dot path", "/job/../queue/item/42/", "", false},
		{"encoded", "/queue/item/%34%32/", "", false},
		{"query", "/queue/item/42/?token=secret", "", false},
		{"empty query", "/queue/item/42/?", "", false},
		{"fragment", "/queue/item/42/#secret", "", false},
		{"empty fragment", "/queue/item/42/#", "", false},
		{"relative path", "queue/item/42/", "", false},
		{"network path", "//example.invalid/queue/item/42/", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			var host string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/job/demo/api/json") {
					_, _ = io.WriteString(w, `{}`)
					return
				}
				if strings.HasSuffix(r.URL.Path, "/crumbIssuer/api/json") {
					w.WriteHeader(404)
					return
				}
				calls.Add(1)
				w.Header().Set("Location", strings.ReplaceAll(tc.location, "$ORIGIN", host))
				w.WriteHeader(http.StatusCreated)
			}))
			defer srv.Close()
			host = srv.URL
			res, err := NewClient(host+tc.prefix, "u", "t").TriggerBuild("demo", nil)
			if tc.valid {
				require.NoError(t, err)
				require.NotNil(t, res)
				assert.Equal(t, 42, res.QueueID)
			} else {
				assert.Error(t, err)
				assert.Nil(t, res)
				if err != nil {
					assert.NotContains(t, err.Error(), "secret")
				}
			}
			assert.Equal(t, int32(1), calls.Load())
		})
	}
}

func TestTriggerBuildNeverReplaysMutation(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308, 403, 503, 0} {
		for _, params := range []map[string]string{nil, {"BRANCH": "main"}} {
			t.Run(fmt.Sprintf("%d/params=%t", code, params != nil), func(t *testing.T) {
				var posts, redirected atomic.Int32
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/api/json") && !strings.Contains(r.URL.Path, "crumbIssuer") {
						_, _ = io.WriteString(w, `{}`)
						return
					}
					if r.URL.Path == "/crumbIssuer/api/json" {
						_, _ = io.WriteString(w, `{"crumb":"c","crumbRequestField":"Jenkins-Crumb"}`)
						return
					}
					if r.URL.Path == "/replayed" {
						redirected.Add(1)
						w.Header().Set("Location", "/queue/item/42/")
						w.WriteHeader(201)
						return
					}
					posts.Add(1)
					assert.Equal(t, "POST", r.Method)
					assert.Equal(t, "c", r.Header.Get("Jenkins-Crumb"))
					if code == 0 {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(code)
				}))
				defer srv.Close()
				_, err := NewClient(srv.URL, "u", "t").TriggerBuild("demo", params)
				require.Error(t, err)
				assert.Equal(t, int32(1), posts.Load())
				assert.Zero(t, redirected.Load())
			})
		}
	}
}

func TestBuildDetailIncludesQueueIdentity(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Contains(t, r.URL.Query().Get("tree"), "queueId")
		_, _ = io.WriteString(w, `{"number":7,"queueId":42}`)
	}))
	defer srv.Close()
	build, err := NewClient(srv.URL, "u", "t").GetBuild("demo", 7)
	require.NoError(t, err)
	raw, err := json.Marshal(build)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"queueId":42`)
}

func TestQueueItemIdentityMustMatch(t *testing.T) {
	for _, body := range []string{`{"id":43}`, `{}`, `{"id":42,"cancelled":true}`} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		client := NewClient(srv.URL, "u", "t")
		item, err := client.GetQueueItem(42)
		if strings.Contains(body, `"id":42`) {
			require.NoError(t, err)
			assert.True(t, item.Cancelled)
		} else {
			require.Error(t, err)
			assert.Nil(t, item)
		}
		srv.Close()
	}
}

func TestPostOnceRequestCannotBeReplayedByTransport(t *testing.T) {
	for _, body := range []io.Reader{nil, strings.NewReader("x=y")} {
		client := NewClient("https://jenkins.example.test", "u", "t")
		client.crumbs.crumb = &crumbInfo{CrumbRequestField: "Idempotency-Key", Crumb: "test"}
		recorder := &originRecordingTransport{}
		client.httpClient.Transport.(*authTransport).base = recorder
		resp, err := client.PostOnce("/job/demo/build", body, "")
		require.NoError(t, err)
		CloseBody(resp)
		require.Len(t, recorder.requests, 1)
		req := recorder.requests[0]
		assert.Nil(t, req.GetBody, "transport must not be able to replay a consumed request body")
		assert.NotNil(t, req.Body, "empty POST must not be classified as replayable")
		assert.NotEqual(t, http.NoBody, req.Body)
	}
}
