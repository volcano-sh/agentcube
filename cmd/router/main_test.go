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
	"github.com/stretchr/testify/require"
	"github.com/volcano-sh/agentcube/pkg/router"
)

func TestMain(m *testing.M) {
	os.Setenv("REDIS_ADDR", "localhost:6379")
	os.Setenv("REDIS_PASSWORD", "fake")
	os.Setenv("WORKLOAD_MANAGER_ADDR", "localhost:8080") // required by router pkg
	code := m.Run()
	os.Exit(code)
}

func TestRouterFlagParsing(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantPort string
		wantDbg  bool
	}{
		{"defaults", []string{}, "8080", false},
		{"custom", []string{"-port", "9090", "-debug"}, "9090", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet(tc.name, flag.ContinueOnError)
			port := fs.String("port", "8080", "")
			debug := fs.Bool("debug", false, "")
			_ = fs.Parse(tc.args)

			assert.Equal(t, tc.wantPort, *port)
			assert.Equal(t, tc.wantDbg, *debug)
		})
	}
}

func TestRouterNewServer(t *testing.T) {
	cfg := &router.Config{
		Port:                  "8080",
		Debug:                 true,
		EnableTLS:             false,
		MaxConcurrentRequests: 500,
	}
	s, err := router.NewServer(cfg)
	require.NoError(t, err)
	assert.NotNil(t, s)
}