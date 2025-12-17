package workloadmanager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRandString(t *testing.T) {
	v := RandString(8)
	assert.Equal(t, 8, len(v))

	v = RandString(32)
	assert.Equal(t, 32, len(v))
}
