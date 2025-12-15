package store

import (
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
)

const (
	redisStoreType  string = "redis"
	valkeyStoreType string = "valkey"
)

var (
	initStoreOnce       = &sync.Once{}
	provider      Store = nil
)

// Storage get store singleton
// support Redis, Valkey, Redis as default, can be setting by env STORE_TYPE
// --- redis STORE_TYPE environments ---
// REDIS_ADDR:     redis address, required
// REDIS_PASSWORD: redis password, required
// --- valkey STORE_TYPE environments ---
// VALKEY_ADDR:          valkey address, required
// VALKEY_PASSWORD:      valkey password, required
// VALKEY_DISABLE_CACHE: disable valkey client cache, optional
// VALKEY_FORCE_SINGLE:  force setting valkey single mode, optional
func Storage() Store {
	initStoreOnce.Do(func() {
		err := initStore()
		if err != nil {
			log.Fatalf("init redis store failed: %v", err)
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
		log.Println("init redis store successfully")
	case valkeyStoreType:
		valkeyProvider, err := initValkeyStore()
		if err != nil {
			return fmt.Errorf("init valkey store failed: %w", err)
		}
		provider = valkeyProvider
		log.Println("init valkey store successfully")
	default:
		return fmt.Errorf("unsupported provider type: %v", providerType)
	}
	return nil
}
