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

package e2b

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/volcano-sh/agentcube/pkg/store"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/v2"
)

// respondWithError sends an E2B formatted error response
func respondWithError(c *gin.Context, code ErrorCode, message string) {
	c.JSON(int(code), Error{
		Code:    int(code),
		Message: message,
	})
}

// mapError maps various error types to E2B error codes and messages
func mapError(err error) (ErrorCode, string) {
	if err == nil {
		return 0, ""
	}

	// Check for rate limit exceeded error
	if errors.Is(err, ErrRateLimitExceeded) {
		return ErrTooManyRequests, "rate limit exceeded"
	}

	// Check for store not found error
	if errors.Is(err, store.ErrNotFound) {
		return ErrNotFound, "sandbox not found"
	}

	// Check for Kubernetes not found errors
	if k8serrors.IsNotFound(err) {
		return ErrNotFound, "resource not found"
	}

	// Check for Kubernetes unauthorized errors
	if k8serrors.IsUnauthorized(err) {
		return ErrUnauthorized, "unauthorized"
	}

	// Check for Kubernetes conflict errors
	if k8serrors.IsConflict(err) {
		return ErrConflict, "conflict"
	}

	// Default to internal server error
	klog.V(4).Infof("mapping unhandled error to internal server error: %v", err)
	return ErrInternal, "internal server error"
}

// handleStoreError handles store errors and returns appropriate HTTP response
func handleStoreError(c *gin.Context, err error) {
	code, message := mapError(err)
	respondWithError(c, code, message)
}

// common error responses
var (
	// ErrMissingAPIKey is returned when the API key is missing
	ErrMissingAPIKey = Error{
		Code:    http.StatusUnauthorized,
		Message: "API key is required",
	}
	// ErrInvalidAPIKey is returned when the API key is invalid
	ErrInvalidAPIKey = Error{
		Code:    http.StatusUnauthorized,
		Message: "invalid API key",
	}
	// ErrSandboxNotFound is returned when the sandbox is not found
	ErrSandboxNotFound = Error{
		Code:    http.StatusNotFound,
		Message: "sandbox not found",
	}
	// ErrTemplateNotFound is returned when the template is not found
	ErrTemplateNotFound = Error{
		Code:    http.StatusBadRequest,
		Message: "template not found",
	}
	// ErrAutoPauseNotSupported is returned when auto_pause is requested (not supported in Phase 1)
	ErrAutoPauseNotSupported = Error{
		Code:    http.StatusBadRequest,
		Message: "auto_pause not supported",
	}
)
