/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestGeneratedCRDValidationSchema(t *testing.T) {
	tests := []struct {
		name    string
		crdFile string
		ports   string
		rule    string
	}{
		{
			name:    "AgentRuntime",
			crdFile: "runtime.agentcube.volcano.sh_agentruntimes.yaml",
			ports:   "targetPort",
			rule:    "!has(self.sessionTimeout) || !has(self.maxSessionDuration) || duration(self.sessionTimeout) <= duration(self.maxSessionDuration)",
		},
		{
			name:    "CodeInterpreter",
			crdFile: "runtime.agentcube.volcano.sh_codeinterpreters.yaml",
			ports:   "ports",
			rule:    "!has(self.sessionTimeout) || !has(self.maxSessionDuration) || duration(self.sessionTimeout) <= duration(self.maxSessionDuration)",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			specSchema := loadCRDSpecSchema(t, tt.crdFile)

			portSchema := nestedMap(t, specSchema, "properties", tt.ports, "items", "properties", "port")
			assertNumber(t, portSchema, "minimum", 1)
			assertNumber(t, portSchema, "maximum", 65535)

			pathPrefixSchema := nestedMap(t, specSchema, "properties", tt.ports, "items", "properties", "pathPrefix")
			assertString(t, pathPrefixSchema, "pattern", "^/.*")

			sessionTimeoutSchema := nestedMap(t, specSchema, "properties", "sessionTimeout")
			assertString(t, sessionTimeoutSchema, "format", "duration")
			assertHasValidationRule(t, sessionTimeoutSchema, "duration(self) > duration('0s')")

			maxSessionDurationSchema := nestedMap(t, specSchema, "properties", "maxSessionDuration")
			assertString(t, maxSessionDurationSchema, "format", "duration")
			assertHasValidationRule(t, maxSessionDurationSchema, "duration(self) > duration('0s')")

			assertHasValidationRule(t, specSchema, tt.rule)
		})
	}
}

func TestGeneratedCodeInterpreterWarmPoolSizeValidation(t *testing.T) {
	specSchema := loadCRDSpecSchema(t, "runtime.agentcube.volcano.sh_codeinterpreters.yaml")
	warmPoolSchema := nestedMap(t, specSchema, "properties", "warmPoolSize")
	assertNumber(t, warmPoolSchema, "minimum", 0)
}

func loadCRDSpecSchema(t *testing.T, crdFile string) map[string]interface{} {
	t.Helper()

	_, currentFile, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file path")
	}
	path := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "..", "manifests", "charts", "base", "crds", crdFile)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read CRD %s: %v", path, err)
	}

	jsonData, err := yaml.ToJSON(data)
	if err != nil {
		t.Fatalf("convert CRD %s to JSON: %v", crdFile, err)
	}

	var crd map[string]interface{}
	if err := json.Unmarshal(jsonData, &crd); err != nil {
		t.Fatalf("decode CRD %s: %v", crdFile, err)
	}

	versionSchema := crdVersionSchema(t, crd, "v1alpha1")
	return nestedMap(t, versionSchema, "openAPIV3Schema", "properties", "spec")
}

func crdVersionSchema(t *testing.T, crd map[string]interface{}, wantName string) map[string]interface{} {
	t.Helper()

	spec := nestedMap(t, crd, "spec")
	versions, ok := spec["versions"].([]interface{})
	if !ok {
		t.Fatalf("spec.versions = %T(%v), want array", spec["versions"], spec["versions"])
	}

	var storageSchema map[string]interface{}
	for _, version := range versions {
		versionMap, ok := version.(map[string]interface{})
		if !ok {
			t.Fatalf("CRD version = %T(%v), want map", version, version)
		}
		schema := nestedMap(t, versionMap, "schema")
		if name, _ := versionMap["name"].(string); name == wantName {
			return schema
		}
		if storage, _ := versionMap["storage"].(bool); storage {
			storageSchema = schema
		}
	}
	if storageSchema != nil {
		return storageSchema
	}
	t.Fatalf("CRD versions do not include %q or a storage version", wantName)
	return nil
}

func nestedMap(t *testing.T, obj map[string]interface{}, path ...string) map[string]interface{} {
	t.Helper()

	current := interface{}(obj)
	for _, key := range path {
		switch typed := current.(type) {
		case map[string]interface{}:
			var ok bool
			current, ok = typed[key]
			if !ok {
				t.Fatalf("missing schema path %v at %q", path, key)
			}
		case []interface{}:
			if key != "0" {
				t.Fatalf("unsupported array path key %q in %v", key, path)
			}
			if len(typed) == 0 {
				t.Fatalf("empty array at path %v", path)
			}
			current = typed[0]
		default:
			t.Fatalf("schema path %v reached non-object %T at %q", path, current, key)
		}
	}

	result, ok := current.(map[string]interface{})
	if !ok {
		t.Fatalf("schema path %v resolved to %T, want map", path, current)
	}
	return result
}

func assertNumber(t *testing.T, schema map[string]interface{}, field string, want float64) {
	t.Helper()

	got, ok := schema[field].(float64)
	if !ok {
		t.Fatalf("schema field %q = %T(%v), want number %v", field, schema[field], schema[field], want)
	}
	if got != want {
		t.Fatalf("schema field %q = %v, want %v", field, got, want)
	}
}

func assertString(t *testing.T, schema map[string]interface{}, field, want string) {
	t.Helper()

	got, ok := schema[field].(string)
	if !ok {
		t.Fatalf("schema field %q = %T(%v), want string %q", field, schema[field], schema[field], want)
	}
	if got != want {
		t.Fatalf("schema field %q = %q, want %q", field, got, want)
	}
}

func assertHasValidationRule(t *testing.T, schema map[string]interface{}, want string) {
	t.Helper()

	validations, ok := schema["x-kubernetes-validations"].([]interface{})
	if !ok {
		t.Fatalf("schema missing x-kubernetes-validations, want rule %q", want)
	}
	for _, validation := range validations {
		validationMap, ok := validation.(map[string]interface{})
		if !ok {
			t.Fatalf("validation entry = %T(%v), want map", validation, validation)
		}
		rule, ok := validationMap["rule"].(string)
		if !ok {
			t.Fatalf("validation rule = %T(%v), want string", validationMap["rule"], validationMap["rule"])
		}
		if normalizeRule(rule) == normalizeRule(want) {
			return
		}
	}
	t.Fatalf("schema validations %v do not include rule %q", validations, want)
}

func normalizeRule(rule string) string {
	return strings.Join(strings.Fields(rule), " ")
}
