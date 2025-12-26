package picod

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJupyterManager_BasicExecution(t *testing.T) {
	// Skip if Jupyter is not installed
	t.Skip("Skipping Jupyter integration test - requires Jupyter Server installed")

	tmpDir := t.TempDir()

	jm, err := NewJupyterManager(tmpDir)
	require.NoError(t, err)
	defer jm.Shutdown()

	// Test basic execution
	result, err := jm.ExecuteCode("print('Hello, World!')", 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "ok", result.Status)
	assert.Contains(t, result.Output, "Hello, World!")
	assert.Empty(t, result.Error)
}

func TestJupyterManager_ErrorHandling(t *testing.T) {
	t.Skip("Skipping Jupyter integration test - requires Jupyter Server installed")

	tmpDir := t.TempDir()

	jm, err := NewJupyterManager(tmpDir)
	require.NoError(t, err)
	defer jm.Shutdown()

	// Test error execution
	result, err := jm.ExecuteCode("undefined_variable", 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "error", result.Status)
	assert.NotEmpty(t, result.Error)
}

func TestJupyterManager_EnvironmentIsolation(t *testing.T) {
	t.Skip("Skipping Jupyter integration test - requires Jupyter Server installed")

	tmpDir := t.TempDir()

	jm, err := NewJupyterManager(tmpDir)
	require.NoError(t, err)
	defer jm.Shutdown()

	// Set a variable
	result1, err := jm.ExecuteCode("x = 42", 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "ok", result1.Status)

	// After soft reset, variable should not exist
	result2, err := jm.ExecuteCode("print(x)", 10*time.Second)
	require.NoError(t, err)
	assert.Equal(t, "error", result2.Status)
	assert.Contains(t, result2.Error, "NameError")
}

func TestJupyterManager_ConcurrentExecution(t *testing.T) {
	t.Skip("Skipping Jupyter integration test - requires Jupyter Server installed")

	tmpDir := t.TempDir()

	jm, err := NewJupyterManager(tmpDir)
	require.NoError(t, err)
	defer jm.Shutdown()

	// Test concurrent execution (should be serialized by mutex)
	done := make(chan bool, 2)

	go func() {
		result, err := jm.ExecuteCode("import time; time.sleep(0.1); print('First')", 10*time.Second)
		require.NoError(t, err)
		assert.Equal(t, "ok", result.Status)
		done <- true
	}()

	go func() {
		result, err := jm.ExecuteCode("print('Second')", 10*time.Second)
		require.NoError(t, err)
		assert.Equal(t, "ok", result.Status)
		done <- true
	}()

	<-done
	<-done
}

func TestJupyterManager_Timeout(t *testing.T) {
	t.Skip("Skipping Jupyter integration test - requires Jupyter Server installed")

	tmpDir := t.TempDir()

	jm, err := NewJupyterManager(tmpDir)
	require.NoError(t, err)
	defer jm.Shutdown()

	// Test timeout
	_, err = jm.ExecuteCode("import time; time.sleep(10)", 1*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeout")
}
