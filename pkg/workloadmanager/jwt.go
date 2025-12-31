package workloadmanager

// Constants for Router's identity resources
// WorkloadManager uses these to inject the public key into PicoD containers
const (
	// PublicKeyConfigMapName is the name of the ConfigMap storing Router's public key
	PublicKeyConfigMapName = "picod-router-public-key"
	// PublicKeyDataKey is the key in the ConfigMap data map for the public key
	PublicKeyDataKey = "public.pem"
)
