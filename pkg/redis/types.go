package redis

// SandboxKind represents the type of sandbox, such as agent or code-interpreter.
type SandboxKind string

const (
	Agent           SandboxKind = "agent"
	CodeInterpreter SandboxKind = "code-interpreter"
)

// WorkloadSpec describes the workload configuration associated with a sandbox.
type WorkloadSpec struct {
	Kind      SandboxKind `json:"kind,omitempty"`
	Name      string      `json:"name,omitempty"`
	Namespace string      `json:"namespace,omitempty"`
}

// Sandbox represents a running sandbox instance and its metadata.
type Sandbox struct {
	SandboxID string       `json:"sandbox_id"`
	IP        string       `json:"ip"`
	Port      int          `json:"port"`
	Endpoint  string       `json:"endpoint"` // e.g. "10.0.0.1:9000"
	Workload  WorkloadSpec `json:"workload"`
}
