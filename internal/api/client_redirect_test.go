package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedRedirectRejectsChangedOrigin(t *testing.T) {
	for _, kind := range []string{"host", "port", "https-downgrade", "after-same-origin-hop"} {
		t.Run(kind, func(t *testing.T) {
			var destinationCalls atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				destinationCalls.Add(1)
				_, _ = io.WriteString(w, "unexpected destination")
			}))
			defer destination.Close()
			target := destination.URL
			if kind == "host" {
				target = strings.Replace(target, "127.0.0.1", "localhost", 1)
			}
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if kind == "after-same-origin-hop" && r.URL.Path == "/start" {
					http.Redirect(w, r, "/next", http.StatusFound)
					return
				}
				http.Redirect(w, r, target, http.StatusFound)
			})
			var origin *httptest.Server
			if kind == "https-downgrade" {
				origin = httptest.NewTLSServer(handler)
			} else {
				origin = httptest.NewServer(handler)
			}
			defer origin.Close()
			client := NewClient(origin.URL, "dummy-user", "dummy-token")
			client.httpClient.Transport.(*authTransport).base = origin.Client().Transport
			u, err := url.Parse(origin.URL)
			require.NoError(t, err)
			client.httpClient.Jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: "dummy-cookie"}})
			req, err := http.NewRequest(http.MethodGet, origin.URL+"/start", nil)
			require.NoError(t, err)
			req.Header.Set("Jenkins-Crumb", "dummy-crumb")
			resp, err := client.httpClient.Do(req)
			CloseBody(resp)
			assert.Error(t, err, "cross-origin redirects must fail before sending any request")
			assert.Zero(t, destinationCalls.Load(), "even an unauthenticated destination request is forbidden")
		})
	}
}

func TestCrumbFetchRejectsCrossOriginRedirect(t *testing.T) {
	var destinationCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
		_, _ = io.WriteString(w, `{"crumb":"dummy-crumb","crumbRequestField":"Jenkins-Crumb"}`)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusFound)
	}))
	defer origin.Close()
	_, err := NewClient(origin.URL, "dummy-user", "dummy-token").crumbs.ensureCrumb()
	assert.Error(t, err)
	assert.Zero(t, destinationCalls.Load())
}

func TestBlockedRedirectIsNotRetried(t *testing.T) {
	var destinationCalls, originCalls atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destinationCalls.Add(1)
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		originCalls.Add(1)
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	resp, err := NewClient(origin.URL, "dummy-user", "dummy-token").Get("/api/json", nil)
	CloseBody(resp)
	assert.Error(t, err)
	assert.Equal(t, int32(1), originCalls.Load(), "an origin-policy rejection is not transient")
	assert.Zero(t, destinationCalls.Load())
}

func TestSameOriginRedirectKeepsAuthenticatedSession(t *testing.T) {
	var finalCalls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "dummy-cookie", Path: "/"})
			http.Redirect(w, r, "/final", http.StatusTemporaryRedirect)
			return
		}
		finalCalls.Add(1)
		user, token, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "dummy-user", user)
		assert.Equal(t, "dummy-token", token)
		assert.Equal(t, "dummy-crumb", r.Header.Get("Jenkins-Crumb"))
		cookie, err := r.Cookie("session")
		if assert.NoError(t, err) {
			assert.Equal(t, "dummy-cookie", cookie.Value)
		}
	}))
	defer origin.Close()
	client := NewClient(origin.URL, "dummy-user", "dummy-token")
	req, err := http.NewRequest(http.MethodPost, origin.URL+"/start", strings.NewReader("payload"))
	require.NoError(t, err)
	req.Header.Set("Jenkins-Crumb", "dummy-crumb")
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	CloseBody(resp)
	assert.Equal(t, int32(1), finalCalls.Load())
}

func TestSameOriginRedirectChainIsBounded(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer origin.Close()
	client := NewClient(origin.URL, "dummy-user", "dummy-token")
	req, err := http.NewRequest(http.MethodGet, origin.URL+"/loop", nil)
	require.NoError(t, err)
	resp, err := client.httpClient.Do(req)
	CloseBody(resp)
	assert.Error(t, err)
	assert.Equal(t, int32(10), calls.Load(), "retain the standard ten-request redirect bound")
}

type originRecordingTransport struct {
	requests []*http.Request
}

func (t *originRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.requests = append(t.requests, req)
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: req}, nil
}

func TestAuthTransportChecksOriginBeforeNetwork(t *testing.T) {
	for _, target := range []string{
		"https://other.example.test/api/json",
		"https://jenkins.example.test:444/api/json",
		"http://jenkins.example.test:443/api/json",
		"https://jenkins.example.test.evil.invalid/api/json",
		"https://other-user:other-token@jenkins.example.test/api/json",
	} {
		t.Run(target, func(t *testing.T) {
			client := NewClient("https://jenkins.example.test", "dummy-user", "dummy-token")
			transport := client.httpClient.Transport.(*authTransport)
			base := &originRecordingTransport{}
			transport.base = base
			req, err := http.NewRequest(http.MethodGet, target, nil)
			require.NoError(t, err)
			req.Header.Set("Cookie", "session=dummy-cookie")
			req.Header.Set("Jenkins-Crumb", "dummy-crumb")
			resp, err := transport.RoundTrip(req)
			CloseBody(resp)
			assert.Error(t, err)
			assert.Empty(t, base.requests, "origin enforcement must also cover direct transport users")
		})
	}
}

func TestAuthTransportRejectsInvalidConfiguredOrigin(t *testing.T) {
	for _, configured := range []string{"", "ftp://jenkins.example.test", "https://:443", "https://jenkins.example.test:invalid"} {
		client := NewClient(configured, "dummy-user", "dummy-token")
		transport := client.httpClient.Transport.(*authTransport)
		base := &originRecordingTransport{}
		transport.base = base
		req, err := http.NewRequest(http.MethodGet, "https://jenkins.example.test/api/json", nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		CloseBody(resp)
		assert.Error(t, err, configured)
		assert.Empty(t, base.requests, configured)
	}
}

func TestAuthTransportPreservesIPv6ZoneIdentity(t *testing.T) {
	client := NewClient("https://[fe80::1%25en0]", "dummy-user", "dummy-token")
	transport := client.httpClient.Transport.(*authTransport)
	base := &originRecordingTransport{}
	transport.base = base
	req, err := http.NewRequest(http.MethodGet, "https://[fe80::1%25EN0]/api/json", nil)
	require.NoError(t, err)
	resp, err := transport.RoundTrip(req)
	CloseBody(resp)
	assert.Error(t, err, "IPv6 scope identifiers are case-sensitive")
	assert.Empty(t, base.requests)
}

func TestAuthTransportAcceptsEquivalentOrigin(t *testing.T) {
	for _, fixture := range [][2]string{
		{"https://jenkins.example.test", "https://JENKINS.example.test:443/api/json"},
		{"http://jenkins.example.test:80/jenkins", "http://jenkins.example.test/jenkins/api/json"},
		{"https://[::1]:443", "https://[::1]/api/json"},
		{"https://jenkins.example.test:0443", "https://jenkins.example.test/api/json"},
	} {
		client := NewClient(fixture[0], "dummy-user", "dummy-token")
		transport := client.httpClient.Transport.(*authTransport)
		base := &originRecordingTransport{}
		transport.base = base
		req, err := http.NewRequest(http.MethodGet, fixture[1], nil)
		require.NoError(t, err)
		resp, err := transport.RoundTrip(req)
		require.NoError(t, err)
		CloseBody(resp)
		require.Len(t, base.requests, 1)
		user, token, ok := base.requests[0].BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "dummy-user", user)
		assert.Equal(t, "dummy-token", token)
	}
}
