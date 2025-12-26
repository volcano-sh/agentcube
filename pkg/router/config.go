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

// LastActivityAnnotationKey is the annotation key for tracking last activity
const LastActivityAnnotationKey = "agentcube.volcano.sh/last-activity"

// Config contains configuration parameters for Router apiserver
type Config struct {
	// Port is the port the API server listens on
	Port string

	// SandboxEndpoints is the list of available sandbox endpoints
	SandboxEndpoints []string

	// Debug enables debug mode
	Debug bool

	// EnableTLS enables HTTPS
	EnableTLS bool

	// TLSCert is the path to the TLS certificate file
	TLSCert string

	// TLSKey is the path to the TLS private key file
	TLSKey string

	// MaxConcurrentRequests limits the number of concurrent requests (0 = unlimited)
	MaxConcurrentRequests int

	// RequestTimeout sets the timeout for individual requests
	RequestTimeout int // seconds

	// MaxIdleConns sets the maximum number of idle connections in the connection pool
	MaxIdleConns int

	// MaxConnsPerHost sets the maximum number of connections per host
	MaxConnsPerHost int
}
