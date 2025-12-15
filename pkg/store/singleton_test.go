package store

import (
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
)

// TestInitStore tests the initStore function with various scenarios
// Cover default redis, specified redis/valkey, unsupported type, and init failure cases
func TestInitStore(t *testing.T) {
	// Scenario 1: No STORE_TYPE env set, default to redis store
	t.Run("default to redis when STORE_TYPE not set", func(t *testing.T) {
		provider = nil
		// Mock initRedisStore to return a successful redis provider
		patches := gomonkey.ApplyFunc(initRedisStore, func() (*redisStore, error) {
			return &redisStore{}, nil
		})
		defer patches.Reset()

		// Execute the function
		err := initStore()

		// Assert results
		assert.NoErrorf(t, err, "initStore should not return error, but go %v", err)
		assert.IsType(t, &redisStore{}, provider, "provider should be redis instance")
	})

	// Scenario 2: STORE_TYPE set to "redis", init redis store
	t.Run("init redis store when STORE_TYPE is redis", func(t *testing.T) {
		provider = nil
		// Set env via t.Setenv (auto restored after test case)
		t.Setenv("STORE_TYPE", "Redis")

		// Mock initRedisStore to return a successful redis provider
		patches := gomonkey.ApplyFunc(initRedisStore, func() (*redisStore, error) {
			return &redisStore{}, nil
		})
		defer patches.Reset()

		// Execute the function
		err := initStore()

		// Assert results
		assert.NoErrorf(t, err, "initStore should not return error, but go %v", err)
		assert.IsType(t, &redisStore{}, provider, "provider should be redis instance")
	})

	// Scenario 3: STORE_TYPE set to "Valkey" (mixed case), init valkey store
	t.Run("init valkey store when STORE_TYPE is valkey (mixed case)", func(t *testing.T) {
		provider = nil
		// Set env with mixed case to test strings.ToLower
		t.Setenv("STORE_TYPE", "Valkey")

		// Mock initValkeyStore to return a successful redis provider
		patches := gomonkey.ApplyFunc(initValkeyStore, func() (*valkeyStore, error) {
			return &valkeyStore{}, nil
		})
		defer patches.Reset()

		// Execute the function
		err := initStore()

		// Assert results
		assert.NoErrorf(t, err, "initStore should not return error, but go %v", err)
		assert.IsType(t, &valkeyStore{}, provider, "provider should be valkey instance")
	})

	// Scenario 4: STORE_TYPE set to "mysql" (unsupported), return error
	t.Run("return error when STORE_TYPE is unsupported (mysql)", func(t *testing.T) {
		// Set unsupported env
		provider = nil
		t.Setenv("STORE_TYPE", "MySQL")

		// Execute
		err := initStore()

		// Assert
		assert.Error(t, err, "initStore should return error for unsupported provider")
		assert.Contains(t, err.Error(), "unsupported provider type: mysql", "error message should contain unsupported type")
		assert.Nil(t, provider, "provider should be nil for unsupported type")
	})

	// Scenario 5: initRedisStore fails, return error
	t.Run("return error when initRedisStore fails", func(t *testing.T) {
		// Set env to redis
		provider = nil
		t.Setenv("STORE_TYPE", redisStoreType)

		// Mock initRedisStore to return error
		expectedErr := assert.AnError
		patches := gomonkey.ApplyFunc(initRedisStore, func() (*redisStore, error) {
			return nil, expectedErr
		})
		defer patches.Reset()

		// Execute
		err := initStore()

		// Assert
		assert.Error(t, err, "initStore should return error when initRedisStore fails")
		assert.Contains(t, err.Error(), "init redis store failed", "error message should contain redis init failure")
		assert.ErrorIs(t, err, expectedErr, "error should wrap the original initRedisStore error")
		assert.Nil(t, provider, "provider should be nil when init fails")
	})

	// Scenario 6: initValkeyStore fails, return error
	t.Run("return error when initValkeyStore fails", func(t *testing.T) {
		// Set env to valkey
		provider = nil
		t.Setenv("STORE_TYPE", valkeyStoreType)

		// Mock initValkeyStore to return error
		expectedErr := assert.AnError
		patches := gomonkey.ApplyFunc(initValkeyStore, func() (*valkeyStore, error) {
			return nil, expectedErr
		})
		defer patches.Reset()

		// Execute
		err := initStore()

		// Assert
		assert.Error(t, err, "initStore should return error when initValkeyStore fails")
		assert.Contains(t, err.Error(), "init valkey store failed", "error message should contain valkey init failure")
		assert.ErrorIs(t, err, expectedErr, "error should wrap the original initValkeyStore error")
		assert.Nil(t, provider, "provider should be nil when init fails")
	})
}
