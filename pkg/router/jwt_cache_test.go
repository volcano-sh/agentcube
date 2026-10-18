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

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/volcano-sh/agentcube/pkg/common/types"
)

const testUserAlice = "alice"

func TestGenerateTokenCached(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	claims := map[string]interface{}{"session_id": "s1", "user_sub": testUserAlice}

	first, err := manager.GenerateTokenCached(sandboxJWTKey{sessionID: "s1", userSub: testUserAlice}, claims)
	require.NoError(t, err)

	t.Run("same key reuses the token", func(t *testing.T) {
		again, err := manager.GenerateTokenCached(sandboxJWTKey{sessionID: "s1", userSub: testUserAlice}, claims)
		require.NoError(t, err)
		assert.Equal(t, first, again)
	})

	t.Run("different key signs a new token", func(t *testing.T) {
		other, err := manager.GenerateTokenCached(sandboxJWTKey{sessionID: "s1", userSub: "bob"},
			map[string]interface{}{"session_id": "s1", "user_sub": "bob"})
		require.NoError(t, err)
		assert.NotEqual(t, first, other)

		mapClaims := jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(other, mapClaims, func(*jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.Equal(t, "bob", mapClaims["user_sub"])
	})

	t.Run("token past the reuse window is signed again", func(t *testing.T) {
		key := sandboxJWTKey{sessionID: "s2"}
		stale, err := manager.GenerateTokenCached(key, map[string]interface{}{"session_id": "s2"})
		require.NoError(t, err)

		manager.tokenMu.Lock()
		manager.tokenCache.Add(key, cachedToken{token: stale, reuseTo: time.Now().Add(-time.Second)})
		manager.tokenMu.Unlock()

		// iat has second resolution, so make sure the new token differs.
		time.Sleep(1100 * time.Millisecond)
		fresh, err := manager.GenerateTokenCached(key, map[string]interface{}{"session_id": "s2"})
		require.NoError(t, err)
		assert.NotEqual(t, stale, fresh)
	})

	t.Run("loading a new private key drops cached tokens", func(t *testing.T) {
		other, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, manager.loadPrivateKeyPEM(other.GetPrivateKeyPEM()))

		token, err := manager.GenerateTokenCached(sandboxJWTKey{sessionID: "s1", userSub: testUserAlice}, claims)
		require.NoError(t, err)
		assert.NotEqual(t, first, token)
		_, err = jwt.Parse(token, func(*jwt.Token) (interface{}, error) { return other.publicKey, nil })
		assert.NoError(t, err)
	})
}

func TestGenerateTokenCachedZeroValueManager(t *testing.T) {
	src, err := NewJWTManager()
	require.NoError(t, err)
	manager := &JWTManager{}
	require.NoError(t, manager.loadPrivateKeyPEM(src.GetPrivateKeyPEM()))

	token, err := manager.GenerateTokenCached("k", map[string]interface{}{"session_id": "s"})
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestGenerateTokenCachedConcurrent(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := manager.GenerateTokenCached(sandboxJWTKey{sessionID: "shared"}, map[string]interface{}{"session_id": "shared"})
				assert.NoError(t, err)
			}
		}()
	}
	wg.Wait()
}

func TestGenerateSandboxJWTReusesTokenPerSessionAndCaller(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)
	s := &Server{jwtManager: manager}

	newCtx := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		return c
	}
	sandbox := func(session string) *types.SandboxInfo {
		return &types.SandboxInfo{Kind: types.SandboxClaimsKind, SessionID: session}
	}

	a1, ok := s.generateSandboxJWT(newCtx(), sandbox("a"))
	require.True(t, ok)
	a2, ok := s.generateSandboxJWT(newCtx(), sandbox("a"))
	require.True(t, ok)
	b1, ok := s.generateSandboxJWT(newCtx(), sandbox("b"))
	require.True(t, ok)

	assert.Equal(t, a1, a2, "same session and caller should reuse the token")
	assert.NotEqual(t, a1, b1, "different session must get its own token")

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(b1, claims, func(*jwt.Token) (interface{}, error) { return manager.publicKey, nil })
	require.NoError(t, err)
	assert.Equal(t, "b", claims["session_id"])
}
