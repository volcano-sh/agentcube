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

package workloadmanager

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitCodeInterpreterSandbox(t *testing.T) {
	// Initialize JWT manager
	jwtManager, err := NewJWTManager()
	require.NoError(t, err)

	server := &Server{
		jwtManager: jwtManager,
	}

	t.Run("Success", func(t *testing.T) {
		sessionID := "test-session-id"
		publicKey := "test-public-key"
		metadata := map[string]string{"key": "value"}

		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify URL
			assert.Equal(t, "/init", r.URL.Path)
			assert.Equal(t, http.MethodPost, r.Method)

			// Verify Headers
			authHeader := r.Header.Get("Authorization")
			assert.True(t, strings.HasPrefix(authHeader, "Bearer "))
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			// Verify Token
			token, err := jwt.Parse(tokenString, func(_ *jwt.Token) (interface{}, error) {
				return jwtManager.publicKey, nil
			})
			assert.NoError(t, err)
			assert.True(t, token.Valid)

			claims, ok := token.Claims.(jwt.MapClaims)
			assert.True(t, ok)
			assert.Equal(t, sessionID, claims["sessionId"])
			assert.Equal(t, publicKey, claims["session_public_key"])
			assert.Equal(t, "sandbox_init", claims["purpose"])

			// Verify Body
			var reqBody SandboxInitRequest
			err = json.NewDecoder(r.Body).Decode(&reqBody)
			assert.NoError(t, err)
			assert.Equal(t, sessionID, reqBody.SessionID)
			assert.Equal(t, metadata, reqBody.Metadata)

			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		err = server.InitCodeInterpreterSandbox(context.Background(), ts.URL, sessionID, publicKey, metadata, 5)
		assert.NoError(t, err)
	})

	t.Run("ServerError", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
		}))
		defer ts.Close()

		err = server.InitCodeInterpreterSandbox(context.Background(), ts.URL, "session", "key", nil, 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "sandbox init failed with status 500")
	})

	t.Run("NetworkError", func(t *testing.T) {
		// Use a closed port or invalid URL
		err = server.InitCodeInterpreterSandbox(context.Background(), "http://localhost:0", "session", "key", nil, 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to send init request")
	})

	t.Run("Timeout", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(200 * time.Millisecond) // Sleep longer than context timeout
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		err = server.InitCodeInterpreterSandbox(ctx, ts.URL, "session", "key", nil, 10)
		assert.Error(t, err)
		assert.True(t, strings.Contains(err.Error(), "context deadline exceeded") || strings.Contains(err.Error(), "canceled") || strings.Contains(err.Error(), "timeout"), "Error should indicate timeout/cancellation: "+err.Error())
	})

	t.Run("JWTSigningFailure", func(t *testing.T) {
		// Create a server with invalid JWT manager (empty private key)
		badServer := &Server{
			jwtManager: &JWTManager{
				privateKey: &rsa.PrivateKey{}, // non-nil, but invalid (N=0)
			},
		}

		err := badServer.InitCodeInterpreterSandbox(context.Background(), "http://localhost", "session", "key", nil, 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to sign JWT token")
	})
}
