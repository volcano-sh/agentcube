package workloadmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMakeRedisOptions_MissingRedisAddr verifies the function returns an error when REDIS_ADDR is empty
func TestMakeRedisOptions_MissingRedisAddr(t *testing.T) {
	// Use t.Setenv to set env var (auto-restored after test)
	t.Setenv("REDIS_ADDR", "")
	t.Setenv("REDIS_PASSWORD", "test-pass")

	// Execute the function
	opts, err := makeRedisOptions()

	// Validate results
	assert.Nil(t, opts, "Expected options to be nil when REDIS_ADDR is missing")
	assert.Error(t, err, "Expected an error for missing REDIS_ADDR")
	assert.Contains(t, err.Error(), "missing env var REDIS_ADDR", "Error message should mention missing REDIS_ADDR")
}

// TestMakeRedisOptions_MissingRedisPassword verifies the function returns an error when REDIS_PASSWORD is empty
func TestMakeRedisOptions_MissingRedisPassword(t *testing.T) {
	// Set env vars with t.Setenv (auto-cleanup)
	t.Setenv("REDIS_ADDR", "localhost:6379")
	t.Setenv("REDIS_PASSWORD", "")

	// Execute the function
	opts, err := makeRedisOptions()

	// Validate results
	assert.Nil(t, opts, "Expected options to be nil when REDIS_PASSWORD is missing")
	assert.Error(t, err, "Expected an error for missing REDIS_PASSWORD")
	assert.Contains(t, err.Error(), "missing env var REDIS_PASSWORD", "Error message should mention missing REDIS_PASSWORD")
}

// TestMakeRedisOptions_Success verifies the function returns valid options when all env vars are provided
func TestMakeRedisOptions_Success(t *testing.T) {
	// Define test values
	testAddr := "127.0.0.1:6379"
	testPassword := "valid-test-pass"

	// Set valid env vars
	t.Setenv("REDIS_ADDR", testAddr)
	t.Setenv("REDIS_PASSWORD", testPassword)

	// Execute the function
	opts, err := makeRedisOptions()

	// Validate results
	assert.NoError(t, err, "Expected no error when env vars are valid")
	assert.NotNil(t, opts, "Expected non-nil options for valid env vars")
	assert.Equal(t, testAddr, opts.Addr, "Options Addr should match test value")
	assert.Equal(t, testPassword, opts.Password, "Options Password should match test value")
	assert.Equal(t, 0, opts.DB, "Default DB should be 0")
}
