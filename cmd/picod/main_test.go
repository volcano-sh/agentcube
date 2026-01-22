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

package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/volcano-sh/agentcube/pkg/picod"
)

// 2048-bit RSA public key
const fakePubKey = `-----BEGIN PUBLIC KEY-----
MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAzBooGZxmRZ/3QXkLU+sd
DzBEu0oS6t9fePwOG6yfgiQ4JTuFYS10oSoD9oU56eWEi5dn7uskoxiWtbN2osa7
bFhYG7+uLzfpGky15GYd5P9o59squRREazcbFsFmcfhnXMA0uJhMIYoi7Ab1P10D
RfHpL0VdMgp1iOkmthCwA0MRNMmuqs4cuewSr5OYpUC27Q8t14U6FPHWQRAmpAM6
4T1dFf/oCTuRtB1VJ18QcuBlXfL9iqsTMD+q+NNFwLaTrrJuhzESTKZrJ5ShSHXy
WjAYqSjXedPb44zRNdww4LyY2vlpjNwwN7yqUctfrJf2a5jc+7/iznHyRkkbFPWQ
0wIDAQAB
-----END PUBLIC KEY-----`

func TestMain(m *testing.M) {
	os.Setenv("PICOD_AUTH_PUBLIC_KEY", fakePubKey)
	code := m.Run()
	os.Exit(code)
}

func TestFlagParsing(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantPort    int
		wantWorkDir string
	}{
		{"defaults", []string{}, 8080, ""},
		{"custom", []string{"-port", "9000", "-workspace", "/tmp"}, 9000, "/tmp"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			port := fs.Int("port", 8080, "")
			workspace := fs.String("workspace", "", "")
			_ = fs.Parse(tc.args)

			assert.Equal(t, tc.wantPort, *port)
			assert.Equal(t, tc.wantWorkDir, *workspace)
		})
	}
}

func TestConfigBuilding(t *testing.T) {
	cfg := picod.Config{Port: 7000, Workspace: "/w"}
	assert.Equal(t, 7000, cfg.Port)
	assert.Equal(t, "/w", cfg.Workspace)
}

func TestNewServer(t *testing.T) {
	s := picod.NewServer(picod.Config{Port: 8087})
	assert.NotNil(t, s)
}