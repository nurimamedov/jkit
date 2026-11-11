package cmd

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ysmaoui/jkit/internal/jenkins"
)

func TestUpgradeInputMasksSecretsWithoutChangingSubmission(t *testing.T) {
	const secret = "upgrade-canary-secret"
	for _, flags := range [][]string{nil, {"--json"}, {"--format", "{{.Parameters}}"}} {
		t.Run(strings.Join(flags, " "), func(t *testing.T) {
			p := choiceParam("DEPLOY_TOKEN", secret)
			q := choiceParam("VALUE", secret)
			q["type"] = "PasswordParameterDefinition"
			f := newInputFixture(t, []map[string]any{inputAction(pendingExec("Gate", "go?", p, q))}, http.StatusOK)
			var out string
			var err error
			stderr := captureStderr(t, func() {
				out, err = executeCmd(t, append([]string{"input", "my-app", "42", "--approve", "-p", "DEPLOY_TOKEN=" + secret, "-p", "VALUE=" + secret}, flags...)...)
			})
			require.NoError(t, err)
			require.NotContains(t, out+stderr, secret)
			require.Len(t, f.posts, 1)
			require.Contains(t, f.posts[0].form.Get("json"), secret, "wire values must survive masking")
		})
	}
	p := jenkins.ParameterDefinition{Name: "DEPLOY_TOKEN", RawChoices: json.RawMessage(`["` + secret + `"]`), Default: &jenkins.ParameterValue{RawValue: json.RawMessage(`"` + secret + `"`)}}
	in := jenkins.PendingInput{ID: "Gate", Parameters: []jenkins.ParameterDefinition{p, {Name: "OTHER"}}}
	err := checkApproveParams(in, map[string]string{"DEPLOY_TOKEN": secret}, "my-app", 42)
	require.Error(t, err)
	require.NotContains(t, err.Error(), secret)
	newInputFixture(t, []map[string]any{inputAction(pendingExec("Gate", "go?", choiceParam("DEPLOY_TOKEN", secret)))}, http.StatusOK)
	out, err := executeCmd(t, "input", "my-app", "42", "--json")
	require.NoError(t, err)
	require.NotContains(t, out, secret)
}

func TestUpgradeDiffMasksURLCredentials(t *testing.T) {
	for _, flags := range [][]string{nil, {"--json"}, {"--format", "{{.Hunks}}"}} {
		history, configs := twoRevisions()
		for k, v := range configs {
			configs[k] = strings.Replace(v, "<disabled>", "<url>https://user:upgrade-canary-secret@git.example/repo%ZZ</url>\n<disabled>", 1)
		}
		srv, _ := revisionServer(t, history, configs)
		defer srv.Close()
		setupTestConfig(t, srv.URL)
		out, err := executeCmd(t, append([]string{"inspect", "team/svc", "--diff"}, flags...)...)
		require.NoError(t, err)
		require.NotContains(t, out, "upgrade-canary-secret")
		require.Contains(t, out, "numToKeep")
	}
}

func TestUpgradeXMLFileIsPrivateAndRejectsSymlink(t *testing.T) {
	srv := configServer(t, "config_branch.xml")
	defer srv.Close()
	setupTestConfig(t, srv.URL)
	dir := t.TempDir()
	dest := filepath.Join(dir, "config.xml")
	require.NoError(t, os.WriteFile(dest, []byte("old"), 0644))
	_, err := executeCmd(t, "inspect", "team/svc", "--xml", "-o", dest)
	require.NoError(t, err)
	info, err := os.Stat(dest)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	require.NoError(t, os.Remove(dest))
	victim := filepath.Join(dir, "victim")
	require.NoError(t, os.WriteFile(victim, []byte("untouched"), 0600))
	require.NoError(t, os.Symlink(victim, dest))
	_, err = executeCmd(t, "inspect", "team/svc", "--xml", "-o", dest)
	require.Error(t, err)
	data, err := os.ReadFile(victim)
	require.NoError(t, err)
	require.Equal(t, "untouched", string(data))
}

func TestUpgradeRecursiveExportRejectsParentSymlink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "child")))
	root, err := os.OpenRoot(dir)
	require.NoError(t, err)
	defer root.Close()
	e := configExport{root: root}
	require.Error(t, e.save("child/nested", []byte("secret XML")))
	entries, err := os.ReadDir(outside)
	require.NoError(t, err)
	require.Empty(t, entries)
}

func TestUpgradeStagesMachineOutputHasNoInformationalWarning(t *testing.T) {
	readFixture(t, nil, map[int][]jenkins.Stage{42: {{ID: "1", Name: "Build", Status: "SUCCESS"}}}, nil)
	var out string
	var err error
	stderr := captureStderr(t, func() { out, err = executeCmd(t, "stages", "demo", "42", "--json") })
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.Empty(t, stderr)
}
