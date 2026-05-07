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
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/volcano-sh/agentcube/pkg/store"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestRespondWithError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		respondWithError(c, ErrInvalidRequest, "test error message")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "test error message")
	assert.Contains(t, w.Body.String(), "400")
}

func TestMapError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		expectedCode ErrorCode
		expectedMsg  string
	}{
		{
			name:         "nil error",
			err:          nil,
			expectedCode: 0,
			expectedMsg:  "",
		},
		{
			name:         "store not found",
			err:          store.ErrNotFound,
			expectedCode: ErrNotFound,
			expectedMsg:  "sandbox not found",
		},
		{
			name:         "k8s not found",
			err:          k8serrors.NewNotFound(schema.GroupResource{Group: "test", Resource: "sandbox"}, "test-sandbox"),
			expectedCode: ErrNotFound,
			expectedMsg:  "resource not found",
		},
		{
			name:         "k8s unauthorized",
			err:          k8serrors.NewUnauthorized("unauthorized"),
			expectedCode: ErrUnauthorized,
			expectedMsg:  "unauthorized",
		},
		{
			name:         "k8s conflict",
			err:          k8serrors.NewConflict(schema.GroupResource{Group: "test", Resource: "sandbox"}, "test-sandbox", errors.New("conflict")),
			expectedCode: ErrConflict,
			expectedMsg:  "conflict",
		},
		{
			name:         "generic error",
			err:          errors.New("some random error"),
			expectedCode: ErrInternal,
			expectedMsg:  "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg := mapError(tt.err)
			assert.Equal(t, tt.expectedCode, code)
			assert.Equal(t, tt.expectedMsg, msg)
		})
	}
}

func TestHandleStoreError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	router := gin.New()
	router.GET("/not-found", func(c *gin.Context) {
		handleStoreError(c, store.ErrNotFound)
	})
	router.GET("/generic-error", func(c *gin.Context) {
		handleStoreError(c, errors.New("generic error"))
	})

	tests := []struct {
		path           string
		expectedStatus int
		expectedMsg    string
	}{
		{
			path:           "/not-found",
			expectedStatus: http.StatusNotFound,
			expectedMsg:    "sandbox not found",
		},
		{
			path:           "/generic-error",
			expectedStatus: http.StatusInternalServerError,
			expectedMsg:    "internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedMsg)
		})
	}
}

func TestErrorConstants(t *testing.T) {
	tests := []struct {
		name         string
		err          Error
		expectedCode int
		expectedMsg  string
	}{
		{
			name:         "ErrMissingAPIKey",
			err:          ErrMissingAPIKey,
			expectedCode: http.StatusUnauthorized,
			expectedMsg:  "API key is required",
		},
		{
			name:         "ErrInvalidAPIKey",
			err:          ErrInvalidAPIKey,
			expectedCode: http.StatusUnauthorized,
			expectedMsg:  "invalid API key",
		},
		{
			name:         "ErrSandboxNotFound",
			err:          ErrSandboxNotFound,
			expectedCode: http.StatusNotFound,
			expectedMsg:  "sandbox not found",
		},
		{
			name:         "ErrTemplateNotFound",
			err:          ErrTemplateNotFound,
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "template not found",
		},
		{
			name:         "ErrAutoPauseNotSupported",
			err:          ErrAutoPauseNotSupported,
			expectedCode: http.StatusBadRequest,
			expectedMsg:  "auto_pause not supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedCode, tt.err.Code)
			assert.Equal(t, tt.expectedMsg, tt.err.Message)
		})
	}
}
