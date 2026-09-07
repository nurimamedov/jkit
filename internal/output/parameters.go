package output

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"

	"github.com/ysmaoui/jkit/internal/jenkins"
)

// ParameterRedactor remembers plaintext before parameter fields are masked, so
// copies in commit messages and other presented text remain protected.
func ParameterRedactor(builds ...*jenkins.Build) *strings.Replacer {
	secrets := map[string]bool{}
	for _, build := range builds {
		for _, action := range build.Actions {
			for _, param := range action.Parameters {
				if IsSecretKey(param.Name) {
					secrets[param.Value()] = true
					transformParameterStrings(param.RawValue, func(value string) string {
						secrets[value] = true
						return value
					})
				}
			}
		}
	}
	delete(secrets, "")
	values := make([]string, 0, len(secrets))
	for value := range secrets {
		values = append(values, value)
	}
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	pairs := make([]string, 0, 2*len(values))
	for _, value := range values {
		pairs = append(pairs, value, MaskSecret(value))
	}
	return strings.NewReplacer(pairs...)
}

func transformParameterStrings(raw json.RawMessage, transform func(string) string) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return raw
	}
	var visit func(any) any
	visit = func(value any) any {
		switch item := value.(type) {
		case string:
			return transform(item)
		case []any:
			for i := range item {
				item[i] = visit(item[i])
			}
		case map[string]any:
			for key := range item {
				item[key] = visit(item[key])
			}
		}
		return value
	}
	encoded, _ := json.Marshal(visit(value))
	return encoded
}

// MaskParameter applies the same secret-name policy as environment output.
func MaskParameter(name, value string) string {
	if IsSecretKey(name) {
		return MaskSecret(value)
	}
	return value
}

// MaskBuildParameters sanitizes a fetched build immediately before presentation.
// Mask RawValue itself so JSON and arbitrary templates cannot expose it.
func MaskBuildParameters(build *jenkins.Build) {
	redact := ParameterRedactor(build).Replace
	for i := range build.Actions {
		for j := range build.Actions[i].Parameters {
			param := &build.Actions[i].Parameters[j]
			if IsSecretKey(param.Name) {
				param.RawValue, _ = json.Marshal(MaskSecret(param.Value()))
			} else {
				param.RawValue = transformParameterStrings(param.RawValue, redact)
			}
		}
		for j := range build.Actions[i].Causes {
			cause := &build.Actions[i].Causes[j]
			cause.ShortDescription = redact(cause.ShortDescription)
		}
	}
	for i := range build.ChangeSets {
		for j := range build.ChangeSets[i].Items {
			change := &build.ChangeSets[i].Items[j]
			change.Message = redact(change.Message)
			change.Author.FullName = redact(change.Author.FullName)
		}
	}
}
