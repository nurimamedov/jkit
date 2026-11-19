package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type artifactTransport func(*http.Request) (*http.Response, error)

func (f artifactTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestArtifactStaticRedirectDropsCredentials(t *testing.T) {
	const jenkins = "https://jenkins.example.com"
	const static = "https://jenkins-static.example.com"
	const name = "profile/x #1.tgz"
	client := NewClient(jenkins, "tester", "fixture")
	location := static + "/signed?token=example"
	first := 0
	client.httpClient.Transport.(*authTransport).base = artifactTransport(func(req *http.Request) (*http.Response, error) {
		first++
		require.NotEmpty(t, req.Header.Get("Authorization"))
		require.Equal(t, "/job/demo/7/artifact/profile/x%20%231.tgz", req.URL.EscapedPath())
		return &http.Response{StatusCode: 302, Request: req, Header: http.Header{"Location": {location}, "Set-Cookie": {"session=example; Domain=example.com"}}, Body: http.NoBody}, nil
	})
	old := http.DefaultTransport
	defer func() { http.DefaultTransport = old }()
	downloads := 0
	http.DefaultTransport = artifactTransport(func(req *http.Request) (*http.Response, error) {
		downloads++
		require.Equal(t, location, req.URL.String())
		for _, h := range []string{"Authorization", "Cookie", "Referer", "Jenkins-Crumb"} {
			require.Empty(t, req.Header.Get(h))
		}
		return &http.Response{StatusCode: 200, Request: req, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data"))}, nil
	})
	body, err := client.OpenArtifactFile(context.Background(), "demo", 7, name)
	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, body.Close())
	require.NoError(t, err)
	require.Equal(t, "data", string(data))
	for _, bad := range []string{"https://foreign.example/f", "http://jenkins-static.example.com/f", "https://user@jenkins-static.example.com/f", "https://jenkins-static.example.com:444/f"} {
		location = bad
		_, err := client.OpenArtifactFile(context.Background(), "demo", 7, name)
		require.ErrorIs(t, err, errOriginBoundary)
	}
	require.Equal(t, 5, first)
	require.Equal(t, 1, downloads)
}

func TestArtifactFileSizeAndPartialResponses(t *testing.T) {
	for _, tc := range []struct {
		code   int
		length string
	}{{206, "1"}, {200, "536870913"}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", tc.length)
			w.WriteHeader(tc.code)
		}))
		client := NewClient(srv.URL, "tester", "fixture")
		_, err := client.OpenArtifactFile(context.Background(), "demo", 42, "x")
		srv.Close()
		require.Error(t, err)
		if tc.code == 200 {
			var sizeError *http.MaxBytesError
			require.ErrorAs(t, err, &sizeError)
		}
	}
}

// Simulate a response without Content-Length without allocating the payload.
type endlessArtifact struct{}

func (endlessArtifact) Read(p []byte) (int, error) { return len(p), nil }

func TestChunkedArtifactReadStopsAtLimit(t *testing.T) {
	client := NewClient("https://jenkins.example.com", "tester", "fixture")
	client.httpClient.Transport.(*authTransport).base = artifactTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{Request: r, StatusCode: 200, ContentLength: -1, Body: io.NopCloser(endlessArtifact{})}, nil
	})
	body, err := client.OpenArtifactFile(context.Background(), "demo", 7, "x")
	require.NoError(t, err)
	defer body.Close()
	n, err := io.Copy(io.Discard, body)
	var sizeError *http.MaxBytesError
	require.ErrorAs(t, err, &sizeError)
	require.EqualValues(t, MaxArtifactFileBytes, n)
}
