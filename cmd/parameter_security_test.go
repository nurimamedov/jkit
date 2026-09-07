package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// All responses and parameter values are synthetic. The fixture includes a
// second action so redaction cannot accidentally stop after the first one.
func parameterSecurityFixture(t *testing.T) {
	t.Helper()
	t.Setenv("JKIT_PIPELINE_SOURCE", "blueocean")
	build := func(number int) map[string]any {
		token, branch := "secret-before", "branch-before"
		if number == 42 {
			token, branch = "secret-after", "branch-after"
		}
		params := []map[string]any{
			{"name": "API_TOKEN", "value": token},
			{"name": "GIT_BRANCH", "value": branch},
			{"name": "PASSWORD_UNCHANGED", "value": "secret-unchanged"},
		}
		if number == 41 {
			params = append(params, map[string]any{"name": "DB_PASSWORD", "value": "secret-removed"})
		}
		return map[string]any{"number": number, "result": "SUCCESS", "actions": []map[string]any{
			{"_class": "hudson.model.ParametersAction", "parameters": params},
			{"_class": "example.ExtraAction", "parameters": []map[string]any{
				{"name": "CREDENTIAL", "value": map[string]any{"nested": "secret-structured"}},
				{"name": "COUNT", "value": 7},
				{"name": "EXACT_INTEGER", "value": json.Number("9007199254740993")},
				{"name": "COPY_NOTE", "value": []any{token}},
			}},
			{"_class": "hudson.model.CauseAction", "causes": []map[string]any{{"shortDescription": "copy " + token}}},
		}, "changeSets": []map[string]any{{"items": []map[string]any{{
			"commitId": "abcdef123456", "msg": "commit-copy " + token + " secret-unchanged secret-structured", "author": map[string]any{"fullName": "author-copy " + token},
		}}}}}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var value any
		switch {
		case strings.Contains(r.URL.Path, "/nodes/"):
			value = []any{map[string]any{"id": "1", "displayName": "stage-copy secret-after", "result": "SUCCESS"}}
		case strings.Contains(r.URL.Path, "/testReport/"):
			value = map[string]any{"suites": []any{}}
		case r.URL.Path == "/job/demo/api/json":
			value = map[string]any{"builds": []any{build(42)}}
		case r.URL.Path == "/job/demo/41/api/json":
			value = build(41)
		default:
			value = build(42)
		}
		_ = json.NewEncoder(w).Encode(value)
	}))
	t.Cleanup(server.Close)
	setupTestConfig(t, server.URL)
}

func TestParameterSecurityDiffEveryFormat(t *testing.T) {
	for _, mode := range []string{"text", "json", "template"} {
		t.Run(mode, func(t *testing.T) {
			parameterSecurityFixture(t)
			args := []string{"diff", "demo", "41", "42"}
			if mode == "json" {
				args = append(args, "--json")
			} else if mode == "template" {
				args = append(args, "--format", "{{range .Params}}{{.Name}}={{.Old}}/{{.New}} {{end}}{{range .Commits}}{{.Message}} {{.Author}}{{end}}")
			}
			out, err := executeCmd(t, args...)
			require.NoError(t, err)
			assert.NotContains(t, out, "secret-")
			assert.Contains(t, out, "API_TOKEN", "different secrets must remain a reported change")
			assert.Contains(t, out, "DB_PASSWORD", "removed secret remains a reported change")
			assert.NotContains(t, out, "PASSWORD_UNCHANGED")
			assert.Contains(t, out, "branch-before")
			assert.Contains(t, out, "branch-after")
			assert.Contains(t, out, "••••••")
			assert.Contains(t, out, "commit-copy", "keep the nonsensitive commit text")
		})
	}
}

func TestParameterSecurityStatusEveryFormat(t *testing.T) {
	for _, recent := range []bool{false, true} {
		for _, mode := range []string{"text", "json", "template"} {
			t.Run(fmt.Sprintf("recent=%v/%s", recent, mode), func(t *testing.T) {
				parameterSecurityFixture(t)
				args := []string{"status", "demo"}
				if !recent {
					args = append(args, "42")
				}
				if mode == "json" {
					args = append(args, "--json")
				} else if mode == "template" {
					template := "{{range .Actions}}{{range .Parameters}}{{.Name}}={{.Value}};{{printf \"%s\" .RawValue}} {{end}}{{range .Causes}}{{.ShortDescription}}{{end}}{{end}}{{range .ChangeSets}}{{range .Items}}{{.Message}} {{.Author.FullName}}{{end}}{{end}}"
					if recent {
						template = "{{range .}}" + template + "{{end}}"
					}
					args = append(args, "--format", template)
				}
				out, err := executeCmd(t, args...)
				require.NoError(t, err)
				assert.NotContains(t, out, "secret-")
				if !recent || mode != "text" {
					assert.Contains(t, out, "branch-after")
					assert.Contains(t, out, "••••••")
				}
				if mode == "json" {
					assert.Contains(t, out, `"value": 7`, "ordinary parameter type is preserved")
					assert.Contains(t, out, `"value": 9007199254740993`, "integer precision is preserved")
				}
			})
		}
	}
}
