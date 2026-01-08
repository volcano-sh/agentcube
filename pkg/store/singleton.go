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

package store

import (
	"fmt"
	"os"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

const (
	redisStoreType  string = "redis"
	valkeyStoreType string = "valkey"
)

var (
	initStoreOnce = &sync.Once{}
	provider      Store
)

// Storage get store singleton
// support Redis, Valkey, Redis as default, can be setting by env STORE_TYPE
// --- redis STORE_TYPE environments ---
// REDIS_ADDR:     redis address, required
// REDIS_PASSWORD: redis password, optional
// --- valkey STORE_TYPE environments ---
// VALKEY_ADDR:          valkey address, required
// VALKEY_PASSWORD:      valkey password, required
// VALKEY_DISABLE_CACHE: disable valkey client cache, optional
// VALKEY_FORCE_SINGLE:  force setting valkey single mode, optional
func Storage() Store {
	initStoreOnce.Do(func() {
		err := initStore()
		if err != nil {
			klog.Fatalf("init store failed: %v", err)
		}
	})
	return provider
}

func initStore() error {
	// Setting storage provider type by env STORE_TYPE
	providerType, exists := os.LookupEnv("STORE_TYPE")
	if exists == false {
		// redis as default
		providerType = redisStoreType
	}
	// case-insensitive
	providerType = strings.ToLower(providerType)
	switch providerType {
	case redisStoreType:
		redisProvider, err := initRedisStore()
		if err != nil {
			return fmt.Errorf("init redis store failed: %w", err)
		}
		provider = redisProvider
		klog.Info("init redis store successfully")
	case valkeyStoreType:
		valkeyProvider, err := initValkeyStore()
		if err != nil {
			return fmt.Errorf("init valkey store failed: %w", err)
		}
		provider = valkeyProvider
		klog.Info("init valkey store successfully")
	default:
		return fmt.Errorf("unsupported provider type: %v", providerType)
	}
	return nil
}
