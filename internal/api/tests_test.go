package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNullTestReportRemainsAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("null"))
	}))
	defer srv.Close()
	report, err := NewClient(srv.URL, "local", "local").GetTestReport("demo", 42)
	require.NoError(t, err)
	require.Nil(t, report, "null is absent, not a successful zero-test report")
}
