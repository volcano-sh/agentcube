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
	"crypto/rsa"
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

// swapSigningKey replaces the manager's key pair without clearing the token
// cache, unlike loadPrivateKeyPEM. RS256 is deterministic, so comparing two
// tokens signed within the same second cannot tell a cached token from a newly
// signed one. After a swap it can: a cached token still verifies with the old
// public key, a newly signed one only with the new key.
func swapSigningKey(t *testing.T, manager *JWTManager) *rsa.PublicKey {
	t.Helper()
	oldKey := manager.publicKey
	other, err := NewJWTManager()
	require.NoError(t, err)
	manager.privateKey = other.privateKey
	manager.publicKey = other.publicKey
	return oldKey
}

func verifiesWith(token string, key *rsa.PublicKey) bool {
	_, err := jwt.Parse(token, func(*jwt.Token) (interface{}, error) { return key, nil })
	return err == nil
}

func TestGenerateSandboxToken(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)
	alice := &Claims{Subject: testUserAlice}

	first, err := manager.GenerateSandboxToken("s1", alice)
	require.NoError(t, err)

	t.Run("same session and caller reuses the token", func(t *testing.T) {
		oldKey := swapSigningKey(t, manager)
		again, err := manager.GenerateSandboxToken("s1", &Claims{Subject: testUserAlice})
		require.NoError(t, err)
		assert.Equal(t, first, again)
		assert.True(t, verifiesWith(again, oldKey), "a reused token is the one signed with the old key")
	})

	t.Run("different caller signs a new token", func(t *testing.T) {
		other, err := manager.GenerateSandboxToken("s1", &Claims{Subject: "bob"})
		require.NoError(t, err)
		assert.NotEqual(t, first, other)

		mapClaims := jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(other, mapClaims, func(*jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.Equal(t, "bob", mapClaims["user_sub"])
		assert.Equal(t, "s1", mapClaims["session_id"])
	})

	t.Run("no caller and empty subject are different tokens", func(t *testing.T) {
		anonymous, err := manager.GenerateSandboxToken("s3", nil)
		require.NoError(t, err)
		emptySub, err := manager.GenerateSandboxToken("s3", &Claims{})
		require.NoError(t, err)

		mapClaims := jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(anonymous, mapClaims, func(*jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.NotContains(t, mapClaims, "user_sub")

		mapClaims = jwt.MapClaims{}
		_, err = jwt.ParseWithClaims(emptySub, mapClaims, func(*jwt.Token) (interface{}, error) {
			return manager.publicKey, nil
		})
		require.NoError(t, err)
		assert.Contains(t, mapClaims, "user_sub")
	})

	t.Run("token past the reuse window is signed again", func(t *testing.T) {
		stale, err := manager.GenerateSandboxToken("s2", nil)
		require.NoError(t, err)

		manager.tokenCache.Add(sandboxTokenKey{sessionID: "s2"}, cachedToken{token: stale, reuseTo: time.Now().Add(-time.Second)})

		swapSigningKey(t, manager)
		fresh, err := manager.GenerateSandboxToken("s2", nil)
		require.NoError(t, err)
		assert.NotEqual(t, stale, fresh)
		assert.True(t, verifiesWith(fresh, manager.publicKey))
	})

	t.Run("loading a new private key drops cached tokens", func(t *testing.T) {
		other, err := NewJWTManager()
		require.NoError(t, err)
		require.NoError(t, manager.loadPrivateKeyPEM(other.GetPrivateKeyPEM()))

		token, err := manager.GenerateSandboxToken("s1", alice)
		require.NoError(t, err)
		assert.NotEqual(t, first, token)
		assert.True(t, verifiesWith(token, other.publicKey))
	})
}

func TestGenerateSandboxTokenZeroValueManager(t *testing.T) {
	src, err := NewJWTManager()
	require.NoError(t, err)
	manager := &JWTManager{}
	require.NoError(t, manager.loadPrivateKeyPEM(src.GetPrivateKeyPEM()))

	token, err := manager.GenerateSandboxToken("s", nil)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestGenerateSandboxTokenConcurrent(t *testing.T) {
	manager, err := NewJWTManager()
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_, err := manager.GenerateSandboxToken("shared", nil)
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
	oldKey := swapSigningKey(t, manager)
	a2, ok := s.generateSandboxJWT(newCtx(), sandbox("a"))
	require.True(t, ok)
	b1, ok := s.generateSandboxJWT(newCtx(), sandbox("b"))
	require.True(t, ok)

	assert.Equal(t, a1, a2, "same session and caller should reuse the token")
	assert.True(t, verifiesWith(a2, oldKey), "a reused token is the one signed with the old key")
	assert.NotEqual(t, a1, b1, "different session must get its own token")

	claims := jwt.MapClaims{}
	_, err = jwt.ParseWithClaims(b1, claims, func(*jwt.Token) (interface{}, error) { return manager.publicKey, nil })
	require.NoError(t, err)
	assert.Equal(t, "b", claims["session_id"])
}
