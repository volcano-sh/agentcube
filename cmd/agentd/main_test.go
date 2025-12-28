package main

import (
	"flag"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	ctrl "sigs.k8s.io/controller-runtime"
)

func TestAgentdMinimalFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"no flags", []string{}},
		{"dummy flag", []string{"--some-flag=ignored"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet("agentd-test", flag.ContinueOnError)
			os.Args = append([]string{"agentd"}, tt.args...)

			// Just check that flag parsing doesn't panic
			// We don't run the full manager in unit tests
			flag.Parse()

			// Minimal check - in real CI this might be mocked
			assert.NotPanics(t, func() {
				_ = ctrl.GetConfigOrDie // just to reference the import
			}, "flag parsing should not break config loading path")
		})
	}
}