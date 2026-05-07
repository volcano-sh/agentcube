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

package picod

import (
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

// EnvdEnvHandler handles GET /envd/env and returns the sandbox environment
// variables as a JSON map.
func (s *Server) EnvdEnvHandler(c *gin.Context) {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			env[parts[0]] = parts[1]
		}
	}
	c.JSON(http.StatusOK, env)
}

// EnvdHealthHandler handles GET /envd/health and returns 204 No Content.
// No authentication is required for this endpoint.
func (s *Server) EnvdHealthHandler(c *gin.Context) {
	c.Status(http.StatusNoContent)
}
