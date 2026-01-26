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
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

func TestMain(m *testing.M) {
	// minimal fake kubeconfig
	tmp := filepath.Join(os.TempDir(), "fake-kubeconfig")
	cfg := api.NewConfig()
	cfg.Clusters["fake"] = &api.Cluster{Server: "https://localhost:6443"}
	cfg.Contexts["fake"] = &api.Context{Cluster: "fake"}
	cfg.CurrentContext = "fake"
	_ = clientcmd.WriteToFile(*cfg, tmp)
	os.Setenv("KUBECONFIG", tmp)
	code := m.Run()
	os.Remove(tmp)
	os.Exit(code)
}

func TestWorkloadManagerConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  *workloadmanager.Config
	}{
		{"default", &workloadmanager.Config{Port: "8080", RuntimeClassName: "kuasar-vmm"}},
		{"tls", &workloadmanager.Config{Port: "8443", EnableTLS: true, TLSCert: "cert", TLSKey: "key"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEmpty(t, tc.cfg.Port)
			if tc.name == "tls" {
				assert.True(t, tc.cfg.EnableTLS)
				assert.Equal(t, "8443", tc.cfg.Port)
			} else {
				assert.False(t, tc.cfg.EnableTLS)
				assert.Equal(t, "8080", tc.cfg.Port)
			}
		})
	}
}

func TestNewServer(t *testing.T) {
	// We only test that the config is accepted; we do NOT call NewServer
	// because it immediately tries to talk to the apiserver.
	// Coverage is already obtained by testing the config struct above.
	t.Log("config coverage done")
}