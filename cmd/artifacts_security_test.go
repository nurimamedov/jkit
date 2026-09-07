package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ysmaoui/jkit/internal/api"
)

func TestArtifactDownloadSafeReplacement(t *testing.T) {
	for _, kind := range []string{"symlink", "regular overwrite", "hardlink", "interrupted overwrite", "interrupted new file"} {
		t.Run(kind, func(t *testing.T) {
			broken := kind == "interrupted overwrite" || kind == "interrupted new file"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/job/demo/42/api/json" {
					_, _ = io.WriteString(w, `{"artifacts":[{"fileName":"report.txt","relativePath":"report.txt"}]}`)
					return
				}
				if broken {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = io.WriteString(w, "downloaded")
			}))
			defer srv.Close()
			client := api.NewClient(srv.URL, "dummy", "dummy")
			dir := t.TempDir()
			path := filepath.Join(dir, "report.txt")
			sentinel := filepath.Join(t.TempDir(), "sentinel")
			require.NoError(t, os.WriteFile(sentinel, []byte("preserve"), 0600))
			switch kind {
			case "symlink":
				require.NoError(t, os.Symlink(sentinel, path))
			case "hardlink":
				require.NoError(t, os.Link(sentinel, path))
			case "regular overwrite", "interrupted overwrite":
				require.NoError(t, os.WriteFile(path, []byte("preserve"), 0600))
			}
			err := downloadArtifact(client, "demo", 42, "report.txt", path)
			if kind == "symlink" || broken {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			contents, err := os.ReadFile(sentinel)
			require.NoError(t, err)
			require.Equal(t, "preserve", string(contents), "a linked target must never be changed")
			contents, err = os.ReadFile(path)
			if kind == "interrupted new file" {
				require.ErrorIs(t, err, os.ErrNotExist)
			} else {
				require.NoError(t, err)
				want := "downloaded"
				if broken || kind == "symlink" {
					want = "preserve"
				}
				require.Equal(t, want, string(contents))
			}
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			wantFiles := 1
			if kind == "interrupted new file" {
				wantFiles = 0
			}
			require.Len(t, entries, wantFiles, "temporary downloads must be cleaned up")
		})
	}
}
