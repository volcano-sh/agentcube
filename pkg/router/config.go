package router

// LastActivityAnnotationKey is the annotation key for tracking last activity
const LastActivityAnnotationKey = "agentcube.volcano.sh/last-activity"

// Config contains configuration parameters for Router apiserver
type Config struct {
	// Port is the port the API server listens on
	Port string

	// SandboxEndpoints is the list of available sandbox endpoints
	SandboxEndpoints []string

	// Debug enables debug mode
	Debug bool

	// EnableTLS enables HTTPS
	EnableTLS bool

	// TLSCert is the path to the TLS certificate file
	TLSCert string

	// TLSKey is the path to the TLS private key file
	TLSKey string

	// MaxConcurrentRequests limits the number of concurrent requests (0 = unlimited)
	MaxConcurrentRequests int

	// RequestTimeout sets the timeout for individual requests
	RequestTimeout int // seconds

	// MaxIdleConns sets the maximum number of idle connections in the connection pool
	MaxIdleConns int

	// MaxConnsPerHost sets the maximum number of connections per host
	MaxConnsPerHost int

	// Redis configuration
	// EnableRedis enables Redis session activity tracking
	EnableRedis bool

	// RedisAddr is the Redis server address (e.g., "localhost:6379")
	RedisAddr string

	// RedisPassword is the Redis password (optional)
	RedisPassword string

	// RedisDB is the Redis database number
	RedisDB int

	// SessionExpireDuration is the duration after which inactive sessions expire
	SessionExpireDuration int // seconds, default 3600 (1 hour)
}
