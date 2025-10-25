package apiserver

// Config contains configuration parameters for apiserver
type Config struct {
	// Port is the port the API server listens on
	Port string

	// Namespace is the Kubernetes namespace where Sandbox CRDs are created
	Namespace string

	// SSHUsername is the default SSH username for connecting to sandbox pods
	SSHUsername string

	// SSHPort is the SSH port on sandbox pods
	SSHPort int

	// RuntimeClassName is the RuntimeClassName for sandbox pods
	RuntimeClassName string

	// EnableTLS enables HTTPS
	EnableTLS bool

	// TLSCert is the path to the TLS certificate file
	TLSCert string

	// TLSKey is the path to the TLS private key file
	TLSKey string

	// AgentCubeAPIServiceAccount is the service account name for agentcube-apiserver
	// This service account has admin privileges to access all sandboxes
	AgentCubeAPIServiceAccount string
}
