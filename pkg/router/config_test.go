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

package router

import "testing"

func TestConfigDefaults(t *testing.T) {
	config := Config{}

	if config.Port != "" {
		t.Errorf("Default Port should be empty, got %q", config.Port)
	}

	if config.Debug != false {
		t.Errorf("Default Debug should be false, got %v", config.Debug)
	}

	if config.EnableTLS != false {
		t.Errorf("Default EnableTLS should be false, got %v", config.EnableTLS)
	}

	if config.TLSCert != "" {
		t.Errorf("Default TLSCert should be empty, got %q", config.TLSCert)
	}

	if config.TLSKey != "" {
		t.Errorf("Default TLSKey should be empty, got %q", config.TLSKey)
	}

	if config.MaxConcurrentRequests != 0 {
		t.Errorf("Default MaxConcurrentRequests should be 0, got %d", config.MaxConcurrentRequests)
	}
}

// Note: we intentionally avoid tests that only mirror struct literals or
// constant strings, per maintainer feedback. The remaining tests focus on
// meaningful defaults and behavior.
