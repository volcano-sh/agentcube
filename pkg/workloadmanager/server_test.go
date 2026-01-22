/*
Copyright The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
you may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package workloadmanager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewTokenCache(t *testing.T) {
	tc := NewTokenCache(10, time.Second)
	assert.NotNil(t, tc)
	
	tc.Set("token1", true, "user1")
	found, auth, user := tc.Get("token1")
	assert.True(t, found)
	assert.True(t, auth)
	assert.Equal(t, "user1", user)
	
	tc.Remove("token1")
	found, _, _ = tc.Get("token1")
	assert.False(t, found)
}

func TestServerSetupRoutes(t *testing.T) {
	s := &Server{}
	s.setupRoutes()
	assert.NotNil(t, s.router)
}

func TestTokenCacheExpiration(t *testing.T) {
	tc := NewTokenCache(10, 10*time.Millisecond)
	tc.Set("token1", true, "user1")
	
	time.Sleep(50 * time.Millisecond)
	found, _, _ := tc.Get("token1")
	assert.False(t, found, "Token should have expired")
}

func TestConfigValidation(t *testing.T) {
	// NewServer fails if config is nil
	server, err := NewServer(nil, nil)
	assert.Error(t, err)
	assert.Nil(t, server)
}
