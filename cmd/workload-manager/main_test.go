// cmd/workload-manager/main_test.go
package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/volcano-sh/agentcube/pkg/workloadmanager"
)

func TestParseWorkloadManagerFlags(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantPort         string
		wantRuntimeClass string
		wantEnableAuth   bool
	}{
		{
			name:             "defaults",
			args:             []string{},
			wantPort:         "8080",
			wantRuntimeClass: "kuasar-vmm",
			wantEnableAuth:   false,
		},
		{
			name: "custom",
			args: []string{
				"-port=9000",
				"-runtime-class-name=wasmedge",
				"-enable-auth",
			},
			wantPort:         "9000",
			wantRuntimeClass: "wasmedge",
			wantEnableAuth:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("wm-test", flag.ContinueOnError)
			os.Args = append([]string{"workload-manager"}, tt.args...)

			port := flag.String("port", "8080", "")
			runtimeClass := flag.String("runtime-class-name", "kuasar-vmm", "")
			enableAuth := flag.Bool("enable-auth", false, "")

			flag.Parse()

			cfg := &workloadmanager.Config{
				Port:             *port,
				RuntimeClassName: *runtimeClass,
				EnableAuth:       *enableAuth,
			}

			assert.Equal(t, tt.wantPort, cfg.Port)
			assert.Equal(t, tt.wantRuntimeClass, cfg.RuntimeClassName)
			assert.Equal(t, tt.wantEnableAuth, cfg.EnableAuth)
		})
	}
}