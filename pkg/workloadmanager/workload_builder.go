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

package workloadmanager

import (
	"context"
	"fmt"
	"maps"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/volcano-sh/agentcube/pkg/api"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/common/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	"k8s.io/utils/ptr"
	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"
)

// Constants for Router's identity resources
// WorkloadManager uses these to inject the public key into PicoD containers
const (
	// IdentitySecretName is the name of the Secret storing Router's keys
	IdentitySecretName = "picod-router-identity" //nolint:gosec // This is a name reference, not a credential
	// PublicKeyDataKey is the key in the Secret data map for the public key
	PublicKeyDataKey = "public.pem"
)

// IdentitySecretNamespace is the namespace where the identity secret is stored
// This is read from AGENTCUBE_NAMESPACE env var
var IdentitySecretNamespace = "default"

func init() {
	if ns := os.Getenv("AGENTCUBE_NAMESPACE"); ns != "" {
		IdentitySecretNamespace = ns
	}
}

// cachedPublicKey stores the public key loaded from Router's Secret
// This allows PicoD pods to be created in any namespace without cross-namespace Secret references
var (
	cachedPublicKey     string
	publicKeyCacheMutex sync.RWMutex
)

// GetCachedPublicKey returns the cached public key, or empty string if not loaded
func GetCachedPublicKey() string {
	publicKeyCacheMutex.RLock()
	defer publicKeyCacheMutex.RUnlock()
	return cachedPublicKey
}

// IsPublicKeyCached returns true if the public key has been successfully loaded
func IsPublicKeyCached() bool {
	publicKeyCacheMutex.RLock()
	defer publicKeyCacheMutex.RUnlock()
	return cachedPublicKey != ""
}

// InitPublicKeyCache starts a background goroutine that continuously tries to load
// the public key from Router's Secret until successful. This handles the case where
// Router hasn't started yet when WorkloadManager starts.
func InitPublicKeyCache(clientset kubernetes.Interface) {
	go func() {
		retryInterval := 100 * time.Millisecond
		maxRetryInterval := 10 * time.Second

		for {
			err := loadPublicKeyFromSecret(clientset)
			if err == nil {
				klog.Infof("loaded public key from secret %s/%s", IdentitySecretNamespace, IdentitySecretName)
				return
			}

			klog.V(2).Infof("Failed to load public key from secret %s/%s: %v. Retrying in %v...", IdentitySecretNamespace, IdentitySecretName, err, retryInterval)
			time.Sleep(retryInterval)

			// Exponential backoff with max limit
			retryInterval = retryInterval * 2
			if retryInterval > maxRetryInterval {
				retryInterval = maxRetryInterval
			}
		}
	}()
}

// loadPublicKeyFromSecret reads the public key from Router's Secret
func loadPublicKeyFromSecret(clientset kubernetes.Interface) error {
	secret, err := clientset.CoreV1().Secrets(IdentitySecretNamespace).Get(
		context.Background(),
		IdentitySecretName,
		metav1.GetOptions{},
	)
	if err != nil {
		return err
	}

	publicKeyData, ok := secret.Data[PublicKeyDataKey]
	if !ok {
		return fmt.Errorf("public key not found in secret %s/%s (key: %s)",
			IdentitySecretNamespace, IdentitySecretName, PublicKeyDataKey)
	}

	publicKeyCacheMutex.Lock()
	cachedPublicKey = string(publicKeyData)
	publicKeyCacheMutex.Unlock()
	return nil
}

type buildSandboxParams struct {
	namespace          string
	workloadName       string
	sandboxName        string
	sessionID          string
	ttl                time.Duration
	idleTimeout        time.Duration
	podSpec            corev1.PodSpec
	podLabels          map[string]string
	podAnnotations     map[string]string
	spiffeHelperImage  string
}

type buildSandboxClaimParams struct {
	namespace           string
	name                string
	sandboxTemplateName string
	sessionID           string
	idleTimeout         time.Duration
	// ownerReference is the reference to the CodeInterpreter that creates this SandboxClaim
	ownerReference *metav1.OwnerReference
}

// buildSandboxObject builds a Sandbox object from parameters
func buildSandboxObject(params *buildSandboxParams) *sandboxv1alpha1.Sandbox {
	if params.ttl == 0 {
		params.ttl = DefaultSandboxTTL
	}
	if params.idleTimeout == 0 {
		params.idleTimeout = DefaultSandboxIdleTimeout
	}

	shutdownTime := metav1.NewTime(time.Now().Add(params.ttl))

	// Allocate fresh maps for copied metadata so we never mutate informer-cached input.
	// Annotations are only copied when params.podAnnotations is non-nil.
	podLabels := make(map[string]string, len(params.podLabels)+2)
	maps.Copy(podLabels, params.podLabels)
	podLabels[SessionIdLabelKey] = params.sessionID
	podLabels[SandboxNameLabelKey] = params.sandboxName

	podAnnotations := make(map[string]string, len(params.podAnnotations))
	maps.Copy(podAnnotations, params.podAnnotations)

	// Create Sandbox object using agent-sandbox types
	sandbox := &sandboxv1alpha1.Sandbox{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.sandboxName,
			Namespace: params.namespace,
			Labels: map[string]string{
				SessionIdLabelKey:    params.sessionID,
				WorkloadNameLabelKey: params.workloadName,
				"managed-by":        "agentcube-workload-manager",
			},
			Annotations: map[string]string{
				IdleTimeoutAnnotationKey: params.idleTimeout.String(),
			},
		},
		Spec: sandboxv1alpha1.SandboxSpec{
			PodTemplate: sandboxv1alpha1.PodTemplate{
				Spec: params.podSpec,
				ObjectMeta: sandboxv1alpha1.PodMetadata{
					Labels:      podLabels,
					Annotations: podAnnotations,
				},
			},
			Lifecycle: sandboxv1alpha1.Lifecycle{
				ShutdownTime: &shutdownTime,
			},
			Replicas: ptr.To[int32](1),
		},
	}
	return sandbox
}

func buildSandboxClaimObject(params *buildSandboxClaimParams) *extensionsv1alpha1.SandboxClaim {
	idleTimeout := params.idleTimeout
	if idleTimeout == 0 {
		idleTimeout = DefaultSandboxIdleTimeout
	}
	sandboxClaim := &extensionsv1alpha1.SandboxClaim{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "extensions.agents.x-k8s.io/v1alpha1",
			Kind:       types.SandboxClaimsKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      params.name,
			Namespace: params.namespace,
			Labels: map[string]string{
				SessionIdLabelKey:   params.sessionID,
				SandboxNameLabelKey: params.name,
			},
			Annotations: map[string]string{
				IdleTimeoutAnnotationKey: idleTimeout.String(),
			},
		},
		Spec: extensionsv1alpha1.SandboxClaimSpec{
			TemplateRef: extensionsv1alpha1.SandboxTemplateRef{
				Name: params.sandboxTemplateName,
			},
		},
	}
	// Set owner reference to the CodeInterpreter that creates this SandboxClaim
	if params.ownerReference != nil {
		sandboxClaim.ObjectMeta.OwnerReferences = []metav1.OwnerReference{*params.ownerReference}
	}
	return sandboxClaim
}

func buildSandboxByAgentRuntime(namespace string, name string, ifm *Informers, enableMTLS bool, spiffeHelperImageOverride string) (*sandboxv1alpha1.Sandbox, *sandboxEntry, error) {
	agentRuntimeKey := namespace + "/" + name
	// TODO(hzxuzhonghu): make use of typed informer, so we don't need to do type conversion below
	runtimeObj, exists, _ := ifm.AgentRuntimeInformer.GetStore().GetByKey(agentRuntimeKey)
	if !exists {
		return nil, nil, api.ErrAgentRuntimeNotFound
	}

	unstructuredObj, ok := runtimeObj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("agent runtime %s type asserting unstructured.Unstructured failed", agentRuntimeKey)
		return nil, nil, fmt.Errorf("agent runtime type asserting failed")
	}

	var agentRuntimeObj runtimev1alpha1.AgentRuntime
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &agentRuntimeObj); err != nil {
		return nil, nil, fmt.Errorf("failed to convert unstructured to AgentRuntime: %w", err)
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", name, RandString(8))

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	podSpec := agentRuntimeObj.Spec.Template.Spec.DeepCopy()
	if podSpec.RuntimeClassName != nil && *podSpec.RuntimeClassName == "" {
		podSpec.RuntimeClassName = nil
	}

	buildParams := &buildSandboxParams{
		namespace:    namespace,
		workloadName: name,
		sandboxName:  sandboxName,
		sessionID:    sessionID,
		podSpec:      *podSpec,
	}
	// Apply labels and annotations from AgentRuntime template
	if agentRuntimeObj.Spec.Template.Labels != nil {
		buildParams.podLabels = agentRuntimeObj.Spec.Template.Labels
	}
	if agentRuntimeObj.Spec.Template.Annotations != nil {
		buildParams.podAnnotations = agentRuntimeObj.Spec.Template.Annotations
	}
	if agentRuntimeObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = agentRuntimeObj.Spec.MaxSessionDuration.Duration
	}
	idleTimeout := DefaultSandboxIdleTimeout
	if agentRuntimeObj.Spec.SessionTimeout != nil {
		idleTimeout = agentRuntimeObj.Spec.SessionTimeout.Duration
	}
	buildParams.idleTimeout = idleTimeout

	if enableMTLS {
		buildParams.spiffeHelperImage = spiffeHelperImageOrDefault(spiffeHelperImageOverride)
		injectMTLSVolumes(buildParams)
	}

	sandbox := buildSandboxObject(buildParams)
	entry := &sandboxEntry{
		Kind:        types.SandboxKind,
		Ports:       agentRuntimeObj.Spec.Ports,
		SessionID:   sessionID,
		IdleTimeout: idleTimeout,
	}
	return sandbox, entry, nil
}

// buildCodeInterpreterEnvVars copies the template env vars and injects the
// public key when authMode is picod.
func buildCodeInterpreterEnvVars(templateEnv []corev1.EnvVar, authMode runtimev1alpha1.AuthModeType) []corev1.EnvVar {
	envVars := make([]corev1.EnvVar, len(templateEnv))
	copy(envVars, templateEnv)
	if authMode == runtimev1alpha1.AuthModePicoD {
		envVars = append(envVars, corev1.EnvVar{
			Name:  "PICOD_AUTH_PUBLIC_KEY",
			Value: GetCachedPublicKey(),
		})
	}
	return envVars
}

func getCodeInterpreterFromInformer(namespace, name string, informer *Informers) (*runtimev1alpha1.CodeInterpreter, error) {
	key := namespace + "/" + name
	// TODO(hzxuzhonghu): make use of typed informer, so we don't need to do type conversion below
	runtimeObj, exists, err := informer.CodeInterpreterInformer.GetStore().GetByKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get code interpreter %s from informer cache: %w", key, err)
	}
	if !exists {
		return nil, api.ErrCodeInterpreterNotFound
	}
	unstructuredObj, ok := runtimeObj.(*unstructured.Unstructured)
	if !ok {
		klog.Errorf("code interpreter %s type asserting unstructured.Unstructured failed", key)
		return nil, fmt.Errorf("code interpreter type asserting failed")
	}

	var codeInterpreterObj runtimev1alpha1.CodeInterpreter
	if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &codeInterpreterObj); err != nil {
		return nil, fmt.Errorf("failed to convert unstructured to CodeInterpreter: %w", err)
	}
	return &codeInterpreterObj, nil
}

func buildSandboxByCodeInterpreter(namespace string, codeInterpreterName string, informer *Informers, enableMTLS bool, spiffeHelperImageOverride string) (*sandboxv1alpha1.Sandbox, *extensionsv1alpha1.SandboxClaim, *sandboxEntry, error) {
	codeInterpreterObjPtr, err := getCodeInterpreterFromInformer(namespace, codeInterpreterName, informer)
	if err != nil {
		return nil, nil, nil, err
	}
	codeInterpreterObj := *codeInterpreterObjPtr


	// Check public key available if authMode is picod
	if codeInterpreterObj.Spec.AuthMode == runtimev1alpha1.AuthModePicoD && !IsPublicKeyCached() {
		return nil, nil, nil, api.ErrPublicKeyMissing
	}

	sessionID := uuid.New().String()
	sandboxName := fmt.Sprintf("%s-%s", codeInterpreterName, RandString(8))

	idleTimeout := DefaultSandboxIdleTimeout
	if codeInterpreterObj.Spec.SessionTimeout != nil {
		idleTimeout = codeInterpreterObj.Spec.SessionTimeout.Duration
	}

	sandboxEntry := &sandboxEntry{
		Kind:        types.SandboxKind,
		Ports:       codeInterpreterObj.Spec.Ports,
		SessionID:   sessionID,
		IdleTimeout: idleTimeout,
	}

	// Set default port for code interpreter if not configured
	if len(sandboxEntry.Ports) == 0 {
		sandboxEntry.Ports = []runtimev1alpha1.TargetPort{
			{
				Port:       8080,
				Protocol:   runtimev1alpha1.ProtocolTypeHTTP,
				PathPrefix: "/",
			},
		}
	}

	if codeInterpreterObj.Spec.WarmPoolSize != nil && *codeInterpreterObj.Spec.WarmPoolSize > 0 {
		// NOTE(mTLS + warm-pool): The SandboxClaim path provisions pods via the external
		// agent-sandbox controller using the SandboxTemplate embedded in the CodeInterpreter CRD.
		// WorkloadManager only creates a placeholder Sandbox (empty PodSpec) here — the real
		// pod spec is never passed through injectMTLSVolumes. If mTLS is required for warm-pool
		// sandboxes, the spiffe-helper sidecar, cert volumes, and --enable-mtls flags must be
		// embedded directly in the CodeInterpreter spec.template by the operator.
		if enableMTLS {
			klog.Warningf("CodeInterpreter %s/%s uses a warm pool (WarmPoolSize=%d) with mTLS enabled. "+
				"The spiffe-helper sidecar will NOT be auto-injected into warm-pool sandboxes. "+
				"Ensure the spiffe-helper sidecar and --enable-mtls flags are configured in the "+
				"CodeInterpreter spec.template directly.",
				namespace, codeInterpreterName, *codeInterpreterObj.Spec.WarmPoolSize)
		}
		sandboxClaim := buildSandboxClaimObject(&buildSandboxClaimParams{
			namespace:           namespace,
			name:                sandboxName,
			sandboxTemplateName: codeInterpreterName,
			sessionID:           sessionID,
			idleTimeout:         idleTimeout,
			ownerReference: &metav1.OwnerReference{
				APIVersion: codeInterpreterObj.APIVersion,
				Kind:       codeInterpreterObj.Kind,
				Name:       codeInterpreterObj.Name,
				UID:        codeInterpreterObj.UID,
			},
		})
		simpleSandbox := &sandboxv1alpha1.Sandbox{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: namespace,
				Name:      sandboxName,
				Labels: map[string]string{
					SessionIdLabelKey: sessionID,
				},
			},
		}
		if codeInterpreterObj.Spec.MaxSessionDuration != nil {
			shutdownTime := metav1.NewTime(time.Now().Add(codeInterpreterObj.Spec.MaxSessionDuration.Duration))
			simpleSandbox.Spec.Lifecycle.ShutdownTime = &shutdownTime
		}
		sandboxEntry.Kind = types.SandboxClaimsKind
		return simpleSandbox, sandboxClaim, sandboxEntry, nil
	}

	// Normalize RuntimeClassName: if it's an empty string, set it to nil
	runtimeClassName := codeInterpreterObj.Spec.Template.RuntimeClassName
	if runtimeClassName != nil && *runtimeClassName == "" {
		runtimeClassName = nil
	}

	envVars := buildCodeInterpreterEnvVars(codeInterpreterObj.Spec.Template.Environment, codeInterpreterObj.Spec.AuthMode)

	podSpec := corev1.PodSpec{
		ImagePullSecrets: codeInterpreterObj.Spec.Template.ImagePullSecrets,
		RuntimeClassName: runtimeClassName,
		Containers: []corev1.Container{
			{
				Name:            "code-interpreter",
				Image:           codeInterpreterObj.Spec.Template.Image,
				ImagePullPolicy: codeInterpreterObj.Spec.Template.ImagePullPolicy,
				Env:             envVars,
				Command:         codeInterpreterObj.Spec.Template.Command,
				Args:            codeInterpreterObj.Spec.Template.Args,
				Resources:       codeInterpreterObj.Spec.Template.Resources,
			},
		},
	}

	buildParams := &buildSandboxParams{
		sandboxName:    sandboxName,
		namespace:      namespace,
		sessionID:      sessionID,
		podSpec:        podSpec,
		podLabels:      codeInterpreterObj.Spec.Template.Labels,
		podAnnotations: codeInterpreterObj.Spec.Template.Annotations,
		idleTimeout:    idleTimeout,
	}

	if enableMTLS {
		buildParams.spiffeHelperImage = spiffeHelperImageOrDefault(spiffeHelperImageOverride)
		injectMTLSVolumes(buildParams)
	}
	if codeInterpreterObj.Spec.MaxSessionDuration != nil {
		buildParams.ttl = codeInterpreterObj.Spec.MaxSessionDuration.Duration
	}
	sandbox := buildSandboxObject(buildParams)
	return sandbox, nil, sandboxEntry, nil
}

const (
	// spireCertVolumeName is the shared emptyDir volume where spiffe-helper writes SVIDs.
	spireCertVolumeName = "spire-certs"
	// spireCertMountPath is where both the sidecar and PicoD container mount the cert volume.
	// Matches the certDir value from values.yaml (spire.spiffeHelper.certDir).
	spireCertMountPath = "/run/spire/certs"
	// spireAgentSocketVolumeName is the volume exposing the SPIRE Agent socket.
	spireAgentSocketVolumeName = "spire-agent-socket"
	// spireAgentSocketPath is the host path where the SPIRE Agent socket lives.
	spireAgentSocketPath = "/run/spire/sockets"
	// spiffeHelperConfigVolumeName is the volume holding the spiffe-helper configuration.
	spiffeHelperConfigVolumeName = "spiffe-helper-config"
	// spiffeHelperConfigAnnotationKey is the pod annotation key used to inject the config inline.
	spiffeHelperConfigAnnotationKey = "agentcube.volcano.sh/spiffe-helper-config"
	// DefaultSPIFFEHelperImage is the default container image for the spiffe-helper sidecar.
	// Operators in air-gapped environments or upgrading versions can override this via
	// --spiffe-helper-image flag (Config.SPIFFEHelperImage).
	DefaultSPIFFEHelperImage = "ghcr.io/spiffe/spiffe-helper:0.8.0"

	// SVID file names written by spiffe-helper (must match values.yaml).
	svidCertFileName   = "svid.pem"
	svidKeyFileName    = "svid_key.pem"
	svidBundleFileName = "svid_bundle.pem"

	// spiffeHelperConfigContent is the inline configuration for the spiffe-helper sidecar.
	spiffeHelperConfigContent = `
agent_address = "/run/spire/sockets/agent.sock"
cmd = ""
cmd_args = ""
cert_dir = "/run/spire/certs"
renew_signal = ""
svid_file_name = "svid.pem"
svid_key_file_name = "svid_key.pem"
svid_bundle_file_name = "svid_bundle.pem"
`
)

// spiffeHelperImageOrDefault returns the override image if non-empty, otherwise DefaultSPIFFEHelperImage.
func spiffeHelperImageOrDefault(override string) string {
	if override != "" {
		return override
	}
	return DefaultSPIFFEHelperImage
}

// injectMTLSVolumes injects a spiffe-helper sidecar and the necessary volumes
// into the pod spec so PicoD can receive dynamically provisioned SPIRE SVIDs.
// It uses DownwardAPI to inline the sidecar configuration.
func injectMTLSVolumes(params *buildSandboxParams) {
	podSpec := &params.podSpec

	// Avoid duplicate injection
	for _, c := range podSpec.Containers {
		if c.Name == "spiffe-helper" {
			klog.V(4).InfoS("Skipping mTLS injection because spiffe-helper container already exists")
			return
		}
	}

	addMTLSVolumes(params, podSpec)
	addMTLSSidecar(params, podSpec)
	injectWorkloadMTLS(podSpec)
}

func addMTLSVolumes(params *buildSandboxParams, podSpec *corev1.PodSpec) {
	existingVolumes := make(map[string]bool)
	for _, v := range podSpec.Volumes {
		existingVolumes[v.Name] = true
	}

	// Add annotation for DownwardAPI inline config
	if params.podAnnotations == nil {
		params.podAnnotations = make(map[string]string)
	}
	params.podAnnotations[spiffeHelperConfigAnnotationKey] = spiffeHelperConfigContent

	if !existingVolumes[spireAgentSocketVolumeName] {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: spireAgentSocketVolumeName,
			VolumeSource: corev1.VolumeSource{
				HostPath: &corev1.HostPathVolumeSource{
					Path: spireAgentSocketPath,
					Type: hostPathType(corev1.HostPathDirectoryOrCreate),
				},
			},
		})
	}
	
	if !existingVolumes[spiffeHelperConfigVolumeName] {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: spiffeHelperConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				DownwardAPI: &corev1.DownwardAPIVolumeSource{
					Items: []corev1.DownwardAPIVolumeFile{
						{
							Path: "spiffe-helper.conf",
							FieldRef: &corev1.ObjectFieldSelector{
								FieldPath: fmt.Sprintf("metadata.annotations['%s']", spiffeHelperConfigAnnotationKey),
							},
						},
					},
				},
			},
		})
	}

	if !existingVolumes[spireCertVolumeName] {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: spireCertVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}
}

func addMTLSSidecar(params *buildSandboxParams, podSpec *corev1.PodSpec) {
	sidecar := corev1.Container{
		Name:            "spiffe-helper",
		Image:           spiffeHelperImageOrDefault(params.spiffeHelperImage),
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"-config", "/etc/spiffe-helper/spiffe-helper.conf"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      spiffeHelperConfigVolumeName,
				MountPath: "/etc/spiffe-helper",
				ReadOnly:  true,
			},
			{
				Name:      spireAgentSocketVolumeName,
				MountPath: spireAgentSocketPath,
				ReadOnly:  true,
			},
			{
				Name:      spireCertVolumeName,
				MountPath: spireCertMountPath,
			},
		},
	}
	podSpec.Containers = append([]corev1.Container{sidecar}, podSpec.Containers...)
}

func injectWorkloadMTLS(podSpec *corev1.PodSpec) {
	injectedMTLSFlags := false
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == "spiffe-helper" {
			continue
		}

		hasCertMount := false
		for _, vm := range podSpec.Containers[i].VolumeMounts {
			if vm.Name == spireCertVolumeName {
				hasCertMount = true
				break
			}
		}
		if !hasCertMount {
			podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts, corev1.VolumeMount{
				Name:      spireCertVolumeName,
				MountPath: spireCertMountPath,
				ReadOnly:  true,
			})
		}

		if podSpec.Containers[i].Name == "picod" || podSpec.Containers[i].Name == "code-interpreter" {
			hasMTLSFlag := false
			for _, arg := range podSpec.Containers[i].Args {
				if arg == "--enable-mtls" {
					hasMTLSFlag = true
					break
				}
			}
			if !hasMTLSFlag {
				podSpec.Containers[i].Args = append(podSpec.Containers[i].Args,
					"--enable-mtls",
					"--mtls-cert-file="+spireCertMountPath+"/"+svidCertFileName,
					"--mtls-key-file="+spireCertMountPath+"/"+svidKeyFileName,
					"--mtls-ca-file="+spireCertMountPath+"/"+svidBundleFileName,
				)
			}
			injectedMTLSFlags = true
		}
	}

	if !injectedMTLSFlags {
		klog.Warningf("mTLS sidecar injected but no container named 'picod' or 'code-interpreter' was found. "+
			"The --enable-mtls flags were NOT injected into any workload container. "+
			"If your container uses a different name, mTLS will not be active despite the cert volume being mounted.")
	}
}

// hostPathType is a helper to get a pointer to a HostPathType.
func hostPathType(t corev1.HostPathType) *corev1.HostPathType {
	return &t
}
