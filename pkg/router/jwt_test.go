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
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewJWTManager(t *testing.T) {
	jm, err := NewJWTManager()
	require.NoError(t, err)
	assert.NotNil(t, jm.privateKey)
	assert.NotNil(t, jm.publicKey)
}

func TestGenerateToken(t *testing.T) {
	jm, _ := NewJWTManager()
	claims := map[string]interface{}{
		"user": "test-user",
	}

	token, err := jm.GenerateToken(claims)
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestGetPEMKeys(t *testing.T) {
	jm, _ := NewJWTManager()
	
	pubPEM, err := jm.GetPublicKeyPEM()
	require.NoError(t, err)
	assert.Contains(t, string(pubPEM), "BEGIN PUBLIC KEY")

	privPEM := jm.GetPrivateKeyPEM()
	assert.Contains(t, string(privPEM), "BEGIN RSA PRIVATE KEY")
}

func TestTryStoreOrLoadJWTKeySecret(t *testing.T) {
	jm, _ := NewJWTManager()
	fakeClient := fake.NewSimpleClientset()
	jm.clientset = fakeClient

	ctx := context.Background()

	// 1. Test creation
	err := jm.TryStoreOrLoadJWTKeySecret(ctx)
	require.NoError(t, err)

	secret, err := fakeClient.CoreV1().Secrets(IdentityNamespace).Get(ctx, IdentitySecretName, metav1.GetOptions{})
	require.NoError(t, err)
	assert.NotEmpty(t, secret.Data[PrivateKeyDataKey])
	assert.NotEmpty(t, secret.Data[PublicKeyDataKey])

	// 2. Test reload from existing
	newJM, _ := NewJWTManager()
	newJM.clientset = fakeClient
	err = newJM.TryStoreOrLoadJWTKeySecret(ctx)
	require.NoError(t, err)
	// Should match the original private key since it reloaded it
	assert.Equal(t, jm.privateKey.D, newJM.privateKey.D)
}

func TestTryStoreOrLoadJWTKeySecret_MissingData(t *testing.T) {
	jm, _ := NewJWTManager()
	fakeClient := fake.NewSimpleClientset()
	jm.clientset = fakeClient
	ctx := context.Background()

	// Create a malformed secret (missing private key)
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      IdentitySecretName,
			Namespace: IdentityNamespace,
		},
		Data: map[string][]byte{
			"other": []byte("data"),
		},
	}
	_, _ = fakeClient.CoreV1().Secrets(IdentityNamespace).Create(ctx, badSecret, metav1.CreateOptions{})

	err := jm.TryStoreOrLoadJWTKeySecret(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private key data not found")
}
