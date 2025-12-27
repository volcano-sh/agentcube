// cmd/router/main_test.go
package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/volcano-sh/agentcube/pkg/router"
)

func TestParseRouterFlags(t *testing.T) {
	tests := []struct {
		name               string
		args               []string
		wantPort           string
		wantEnableTLS      bool
		wantDebug          bool
		wantMaxConcurrent  int
		wantRequestTimeout int
	}{
		{
			name:              "defaults",
			args:              []string{},
			wantPort:          "8080",
			wantEnableTLS:     false,
			wantDebug:         false,
			wantMaxConcurrent: 1000,
			wantRequestTimeout: 30,
		},
		{
			name: "custom",
			args: []string{
				"-port=8443",
				"-enable-tls",
				"-debug",
				"-max-concurrent-requests=400",
			},
			wantPort:           "8443",
			wantEnableTLS:      true,
			wantDebug:          true,
			wantMaxConcurrent:  400,
			wantRequestTimeout: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("router-test", flag.ContinueOnError)
			os.Args = append([]string{"router"}, tt.args...)

			port := flag.String("port", "8080", "")
			enableTLS := flag.Bool("enable-tls", false, "")
			debug := flag.Bool("debug", false, "")
			maxConcurrent := flag.Int("max-concurrent-requests", 1000, "")
			requestTimeout := flag.Int("request-timeout", 30, "")

			flag.Parse()

			cfg := &router.Config{
				Port:                  *port,
				EnableTLS:             *enableTLS,
				Debug:                 *debug,
				MaxConcurrentRequests: *maxConcurrent,
				RequestTimeout:        *requestTimeout,
			}

			assert.Equal(t, tt.wantPort, cfg.Port)
			assert.Equal(t, tt.wantEnableTLS, cfg.EnableTLS)
			assert.Equal(t, tt.wantDebug, cfg.Debug)
			assert.Equal(t, tt.wantMaxConcurrent, cfg.MaxConcurrentRequests)
			assert.Equal(t, tt.wantRequestTimeout, cfg.RequestTimeout)
		})
	}
}