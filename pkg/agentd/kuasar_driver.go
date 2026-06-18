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

package agentd

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"k8s.io/klog/v2"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
)

const (
	// KuasarProviderName is the stable provider identifier for the Kuasar SnapStart driver.
	KuasarProviderName = "snapstart.kuasar.io"

	// defaultKuasarSocketPath is the default path for the Kuasar admin socket.
	defaultKuasarSocketPath = "/run/vmm-sandboxer-admin.sock"

	// kuasarReadinessTimeout is the maximum time to wait for the runtime readiness probe.
	kuasarReadinessTimeout = 5 * time.Minute

	// kuasarReadinessPollInterval is the polling interval for the readiness probe.
	kuasarReadinessPollInterval = 2 * time.Second
)

// KuasarDriver implements SnapshotDriver for Kuasar WarmFork snapshots.
// It connects to the node-local Kuasar admin socket to drive snapshot creation.
//
// Integration note: Kuasar uses an inject-socket readiness sequence before accepting
// snapshot commands. The driver performs the CAPABILITIES -> PREPARE -> READY -> COMMIT
// handshake. When Create() returns, the artifact is detached from the build Sandbox
// lifecycle and safe to restore after the Sandbox is deleted.
type KuasarDriver struct {
	// SocketPath is the path to the Kuasar admin Unix socket.
	SocketPath string
}

// NewKuasarDriver creates a KuasarDriver with the given socket path.
// Pass an empty string to use the default path.
func NewKuasarDriver(socketPath string) *KuasarDriver {
	if socketPath == "" {
		socketPath = defaultKuasarSocketPath
	}
	return &KuasarDriver{SocketPath: socketPath}
}

func (d *KuasarDriver) Name() string { return KuasarProviderName }

func (d *KuasarDriver) Capabilities(_ context.Context) SnapshotDriverCapabilities {
	return SnapshotDriverCapabilities{
		SnapshotModes: []runtimev1alpha1.SandboxSnapshotMode{
			runtimev1alpha1.SandboxSnapshotModeFork,
		},
	}
}

// Create drives the Kuasar SnapStart snapshot creation for one build Sandbox.
// It blocks until the runtime has reached a fork-safe point and the artifact is ready.
func (d *KuasarDriver) Create(ctx context.Context, req SnapshotDriverCreateRequest) (*SnapshotDriverArtifact, error) {
	klog.V(2).InfoS("kuasar driver: creating snapshot",
		"snapshotKey", req.SnapshotKey,
		"sandbox", req.TargetSandboxRef.Name,
		"mode", req.SnapshotMode)

	// Connect to the Kuasar admin socket.
	conn, err := d.dialSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("kuasar driver: connect to %s: %w", d.SocketPath, err)
	}
	defer conn.Close()

	// Perform the CAPABILITIES -> PREPARE -> READY -> COMMIT handshake.
	// The sandbox name is used to identify the target VM in Kuasar.
	sandboxID := req.TargetSandboxRef.Name
	if err := d.performHandshake(ctx, conn, sandboxID, req.SnapshotKey, req.SnapshotMode); err != nil {
		return nil, fmt.Errorf("kuasar driver: handshake for sandbox %s: %w", sandboxID, err)
	}

	klog.V(2).InfoS("kuasar driver: snapshot created",
		"snapshotKey", req.SnapshotKey,
		"sandbox", sandboxID)

	return &SnapshotDriverArtifact{
		ProviderName: KuasarProviderName,
		SnapshotKey:  req.SnapshotKey,
		SnapshotHash: req.SnapshotHash,
		// ProviderRef is the snapshot key; Kuasar resolves it via node-local metadata.
		ProviderRef: req.SnapshotKey,
	}, nil
}

func (d *KuasarDriver) Delete(ctx context.Context, artifact SnapshotDriverArtifact) error {
	conn, err := d.dialSocket(ctx)
	if err != nil {
		return fmt.Errorf("kuasar driver: connect for delete: %w", err)
	}
	defer conn.Close()
	return d.sendDeleteCommand(ctx, conn, artifact.SnapshotKey)
}

func (d *KuasarDriver) List(ctx context.Context) ([]SnapshotDriverArtifact, error) {
	conn, err := d.dialSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("kuasar driver: connect for list: %w", err)
	}
	defer conn.Close()
	return d.sendListCommand(ctx, conn)
}

func (d *KuasarDriver) Inspect(ctx context.Context, artifact SnapshotDriverArtifact) (*SnapshotDriverArtifactStatus, error) {
	conn, err := d.dialSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("kuasar driver: connect for inspect: %w", err)
	}
	defer conn.Close()
	return d.sendInspectCommand(ctx, conn, artifact.SnapshotKey)
}

// dialSocket establishes a Unix socket connection to the Kuasar admin socket.
func (d *KuasarDriver) dialSocket(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", d.SocketPath)
	if err != nil {
		return nil, fmt.Errorf("dial unix %s: %w", d.SocketPath, err)
	}
	_ = conn.SetDeadline(time.Now().Add(kuasarReadinessTimeout + time.Minute))
	return conn, nil
}

// performHandshake executes the Kuasar inject-socket protocol sequence.
// The sequence is: CAPABILITIES -> PREPARE -> READY (wait for runtime) -> COMMIT -> STARTED.
//
// TODO(maintainer): Replace the stub JSON framing below with the actual Kuasar
// admin-socket protocol once the upstream API is stabilized. The steps and
// semantics match the design document (§7.6).
func (d *KuasarDriver) performHandshake(ctx context.Context, conn net.Conn, sandboxID, snapshotKey string, mode runtimev1alpha1.SandboxSnapshotMode) error {
	rd := bufio.NewReader(conn)

	// Step 1: CAPABILITIES — exchange supported actions.
	if err := d.sendCommand(conn, kuasarCommand{Action: "CAPABILITIES", SandboxID: sandboxID}); err != nil {
		return fmt.Errorf("CAPABILITIES send: %w", err)
	}
	if _, err := d.readResponse(rd); err != nil {
		return fmt.Errorf("CAPABILITIES response: %w", err)
	}

	// Step 2: PREPARE — declare intent to snapshot.
	if err := d.sendCommand(conn, kuasarCommand{
		Action:      "PREPARE",
		SandboxID:   sandboxID,
		SnapshotKey: snapshotKey,
		Mode:        string(mode),
	}); err != nil {
		return fmt.Errorf("PREPARE send: %w", err)
	}
	if _, err := d.readResponse(rd); err != nil {
		return fmt.Errorf("PREPARE response: %w", err)
	}

	// Step 3: READY — wait for the runtime to signal that it is at a fork-safe point.
	// The runtime signals readiness via the inject socket; we poll with a timeout.
	readinessCtx, cancel := context.WithTimeout(ctx, kuasarReadinessTimeout)
	defer cancel()
	if err := d.waitForReadiness(readinessCtx, conn, rd, sandboxID); err != nil {
		return fmt.Errorf("waiting for runtime readiness: %w", err)
	}

	// Step 4: COMMIT — trigger the VMM snapshot.
	if err := d.sendCommand(conn, kuasarCommand{Action: "COMMIT", SandboxID: sandboxID}); err != nil {
		return fmt.Errorf("COMMIT send: %w", err)
	}
	if _, err := d.readResponse(rd); err != nil {
		return fmt.Errorf("COMMIT response: %w", err)
	}

	return nil
}

// waitForReadiness polls the runtime readiness signal via the inject socket.
// For Fork mode, "ready" means bootstrap is complete and no user state has been loaded.
func (d *KuasarDriver) waitForReadiness(ctx context.Context, conn net.Conn, rd *bufio.Reader, sandboxID string) error {
	ticker := time.NewTicker(kuasarReadinessPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for runtime readiness for sandbox %s", sandboxID)
		case <-ticker.C:
			if err := d.sendCommand(conn, kuasarCommand{Action: "READY", SandboxID: sandboxID}); err != nil {
				return fmt.Errorf("READY send: %w", err)
			}
			resp, err := d.readResponse(rd)
			if err != nil {
				return fmt.Errorf("READY response: %w", err)
			}
			if resp.Ready {
				return nil
			}
		}
	}
}

// kuasarCommand is the wire format for commands sent to the Kuasar admin socket.
// The actual wire format is Kuasar-internal; this struct is a placeholder.
type kuasarCommand struct {
	Action      string `json:"action"`
	SandboxID   string `json:"sandboxId"`
	SnapshotKey string `json:"snapshotKey,omitempty"`
	Mode        string `json:"mode,omitempty"`
}

// kuasarResponse is the wire format for responses from the Kuasar admin socket.
type kuasarResponse struct {
	Ready bool   `json:"ready"`
	Error string `json:"error,omitempty"`
}

func (d *KuasarDriver) sendCommand(conn net.Conn, cmd kuasarCommand) error {
	// TODO(maintainer): replace with actual Kuasar wire protocol framing once stabilized.
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal command: %w", err)
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("write command: %w", err)
	}
	return nil
}

func (d *KuasarDriver) readResponse(rd *bufio.Reader) (*kuasarResponse, error) {
	// TODO(maintainer): replace with actual Kuasar wire protocol framing once stabilized.
	line, err := rd.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	resp := &kuasarResponse{}
	if err := json.Unmarshal([]byte(line), resp); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("kuasar error: %s", resp.Error)
	}
	return resp, nil
}

func (d *KuasarDriver) sendDeleteCommand(_ context.Context, conn net.Conn, snapshotKey string) error {
	return d.sendCommand(conn, kuasarCommand{Action: "DELETE", SnapshotKey: snapshotKey})
}

func (d *KuasarDriver) sendListCommand(_ context.Context, _ net.Conn) ([]SnapshotDriverArtifact, error) {
	// TODO(maintainer): implement list via Kuasar admin socket.
	return nil, nil
}

func init() {
	RegisterDriverFactory(func() SnapshotDriver {
		return NewKuasarDriver("")
	})
}

func (d *KuasarDriver) sendInspectCommand(_ context.Context, conn net.Conn, snapshotKey string) (*SnapshotDriverArtifactStatus, error) {
	if err := d.sendCommand(conn, kuasarCommand{Action: "INSPECT", SnapshotKey: snapshotKey}); err != nil {
		return nil, err
	}
	if _, err := d.readResponse(bufio.NewReader(conn)); err != nil {
		return nil, err
	}
	// TODO(maintainer): map Kuasar inspect response fields to artifact status.
	return &SnapshotDriverArtifactStatus{Phase: runtimev1alpha1.SnapshotArtifactPhaseReady}, nil
}
