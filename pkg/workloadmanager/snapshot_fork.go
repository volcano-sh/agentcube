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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"
	extensionsv1alpha1 "sigs.k8s.io/agent-sandbox/extensions/api/v1alpha1"

	"k8s.io/klog/v2"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/store"
)

// ForkModeHandler implements SnapshotModeHandler for Fork-mode snapshots.
// It creates a per-node build Sandbox from a SandboxTemplate, snapshots it,
// then makes the artifact available for 1:N forking by new sessions.
type ForkModeHandler struct {
	Client        client.Client
	ArtifactStore store.ArtifactStore
	Recorder      record.EventRecorder
	Scheme        *k8sruntime.Scheme
}

func (h *ForkModeHandler) ComputeHash(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass) (string, error) {
	tmpl := &extensionsv1alpha1.SandboxTemplate{}
	if err := h.Client.Get(ctx, types.NamespacedName{Name: ss.Spec.SourceRef.Name, Namespace: ss.Namespace}, tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return "", fmt.Errorf("source SandboxTemplate %q not found", ss.Spec.SourceRef.Name)
		}
		return "", fmt.Errorf("get source SandboxTemplate %q: %w", ss.Spec.SourceRef.Name, err)
	}
	return computeSnapshotHash(ss, tmpl.Spec.PodTemplate.Spec, sc)
}

func (h *ForkModeHandler) PrepareArtifactSet(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, _ *runtimev1alpha1.SnapshotClass, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, currentHash string) (string, error) {
	var err error
	rawVersion, err = h.clearStaleActiveSet(ctx, ss, manifest, ownerKey, rawVersion, currentHash)
	if err != nil {
		return rawVersion, err
	}
	rawVersion, err = h.maybeStartBackgroundRebuild(ctx, ss, manifest, ownerKey, rawVersion, currentHash)
	if err != nil {
		return rawVersion, err
	}
	return h.ensurePendingSet(ctx, ss, manifest, ownerKey, rawVersion, currentHash)
}

func (h *ForkModeHandler) EnsureTasks(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, workingKey string, artifactSet store.SnapshotArtifactSet) (string, error) {
	targetNodes, err := h.selectTargetNodes(ctx, sc)
	if err != nil {
		return rawVersion, fmt.Errorf("select target nodes: %w", err)
	}
	coveredNodes := coveredArtifactNodes(artifactSet.Artifacts)
	addedArtifacts := false
	for _, nodeName := range targetNodes {
		if _, covered := coveredNodes[nodeName]; covered {
			continue
		}
		if err := h.ensureBuildSandboxAndTask(ctx, ss, sc, artifactSet.SnapshotKey, artifactSet.SnapshotHash, nodeName); err != nil {
			log.FromContext(ctx).Error(err, "failed to ensure build sandbox and task", "node", nodeName)
			continue
		}
		artifactSet.Artifacts = append(artifactSet.Artifacts, newCreatingArtifact(sc.Spec.ProviderName, nodeName, artifactSet))
		addedArtifacts = true
	}
	if !addedArtifacts {
		return rawVersion, nil
	}
	manifest.ArtifactSets[workingKey] = artifactSet
	return saveManifest(ctx, h.ArtifactStore, ownerKey, manifest, rawVersion)
}

func (h *ForkModeHandler) ReadyToPromote(pending store.SnapshotArtifactSet) bool {
	return allNodeArtifactsReady(pending.Artifacts)
}

func (h *ForkModeHandler) CleanupTask(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, task *runtimev1alpha1.SandboxSnapshotTask) error {
	sbName := buildSandboxName(ss.Name, task.Spec.TargetNodeName)
	buildSandbox := &sandboxv1alpha1.Sandbox{}
	if err := h.Client.Get(ctx, types.NamespacedName{Name: sbName, Namespace: ss.Namespace}, buildSandbox); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get build sandbox %s: %w", sbName, err)
	}
	if err := h.Client.Delete(ctx, buildSandbox); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete build sandbox %s: %w", sbName, err)
	}
	return nil
}

func (h *ForkModeHandler) CleanupAll(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot) error {
	sandboxList := &sandboxv1alpha1.SandboxList{}
	if err := h.Client.List(ctx, sandboxList, client.InNamespace(ss.Namespace), client.MatchingLabels{
		runtimev1alpha1.SnapshotNameLabelKey:  ss.Name,
		runtimev1alpha1.SnapshotBuildLabelKey: "true",
	}); err != nil {
		return fmt.Errorf("list build sandboxes: %w", err)
	}
	for i := range sandboxList.Items {
		sb := &sandboxList.Items[i]
		if err := h.Client.Delete(ctx, sb); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete build sandbox %s: %w", sb.Name, err)
		}
	}
	return nil
}

func (h *ForkModeHandler) clearStaleActiveSet(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, currentHash string) (string, error) {
	if !forkRebuildsOnSourceChange(ss) {
		return rawVersion, nil
	}
	activeSet := activeArtifactSet(manifest)
	if activeSet == nil || activeSet.SnapshotHash == currentHash {
		return rawVersion, nil
	}
	log.FromContext(ctx).Info("snapshot hash changed, clearing active artifact set", "snapshot", ss.Name)
	h.Recorder.Event(ss, corev1.EventTypeNormal, "SandboxSnapshotRebuilding", "source change detected; clearing active artifact set")
	manifest.ActiveSetRef = store.SnapshotArtifactSetRef{}
	delete(manifest.ArtifactSets, activeSet.SnapshotKey)
	return saveManifest(ctx, h.ArtifactStore, ownerKey, manifest, rawVersion)
}

func (h *ForkModeHandler) maybeStartBackgroundRebuild(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, currentHash string) (string, error) {
	if ss.Spec.ForkPolicy == nil || ss.Spec.ForkPolicy.RebuildAfter == nil || ss.Status.ReadyAt == nil {
		return rawVersion, nil
	}
	if activeArtifactSet(manifest) == nil || manifest.PendingSetRef.SnapshotKey != "" {
		return rawVersion, nil
	}
	if time.Since(ss.Status.ReadyAt.Time) <= ss.Spec.ForkPolicy.RebuildAfter.Duration {
		return rawVersion, nil
	}
	log.FromContext(ctx).Info("rebuildAfter elapsed, starting background replacement", "snapshot", ss.Name)
	h.Recorder.Event(ss, corev1.EventTypeNormal, "SandboxSnapshotRebuilding", "rebuildAfter elapsed; starting background replacement")
	pendingKey := startNewArtifactSet(ss, manifest, currentHash)
	manifest.PendingSetRef = store.SnapshotArtifactSetRef{SnapshotKey: pendingKey}
	return saveManifest(ctx, h.ArtifactStore, ownerKey, manifest, rawVersion)
}

func (h *ForkModeHandler) ensurePendingSet(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, currentHash string) (string, error) {
	if activeArtifactSet(manifest) != nil || manifest.ActiveSetRef.SnapshotKey != "" || manifest.PendingSetRef.SnapshotKey != "" {
		return rawVersion, nil
	}
	log.FromContext(ctx).Info("no active artifact set, starting initial build", "snapshot", ss.Name)
	h.Recorder.Event(ss, corev1.EventTypeNormal, "SandboxSnapshotCreating", "starting initial snapshot build")
	pendingKey := startNewArtifactSet(ss, manifest, currentHash)
	manifest.PendingSetRef = store.SnapshotArtifactSetRef{SnapshotKey: pendingKey}
	return saveManifest(ctx, h.ArtifactStore, ownerKey, manifest, rawVersion)
}

func (h *ForkModeHandler) selectTargetNodes(ctx context.Context, sc *runtimev1alpha1.SnapshotClass) ([]string, error) {
	nodeList := &corev1.NodeList{}
	sel := labels.SelectorFromSet(sc.Spec.NodeSelector)
	if err := h.Client.List(ctx, nodeList, &client.ListOptions{LabelSelector: sel}); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var result []string
	for _, node := range nodeList.Items {
		if !nodeIsReady(&node) {
			continue
		}
		capLabel := runtimev1alpha1.SnapshotProviderLabelPrefix + sc.Spec.ProviderName
		if node.Labels[capLabel] != "true" {
			continue
		}
		result = append(result, node.Name)
	}
	return result, nil
}

func (h *ForkModeHandler) ensureBuildSandboxAndTask(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass, snapshotKey, snapshotHash, nodeName string) error {
	sbName := buildSandboxName(ss.Name, nodeName)
	taskName := buildTaskName(nodeName, snapshotKey)

	existingTask := &runtimev1alpha1.SandboxSnapshotTask{}
	err := h.Client.Get(ctx, types.NamespacedName{Name: taskName, Namespace: ss.Namespace}, existingTask)
	if err == nil {
		_, err = h.ensureBuildSandbox(ctx, ss, snapshotKey, nodeName, sbName)
		return err
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get snapshot task %s: %w", taskName, err)
	}

	buildSandbox, err := h.ensureBuildSandbox(ctx, ss, snapshotKey, nodeName, sbName)
	if err != nil {
		return err
	}

	task := &runtimev1alpha1.SandboxSnapshotTask{
		ObjectMeta: metaWithLabels(taskName, ss.Namespace, map[string]string{
			runtimev1alpha1.SnapshotNameLabelKey: ss.Name,
			runtimev1alpha1.SnapshotKeyLabelKey:  snapshotKey,
			runtimev1alpha1.SnapshotNodeLabelKey: nodeName,
		}),
		Spec: runtimev1alpha1.SandboxSnapshotTaskSpec{
			SnapshotRef: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To(runtimev1alpha1.GroupVersion.Group),
				Kind:     "SandboxSnapshot",
				Name:     ss.Name,
			},
			SnapshotUID:  ss.UID,
			SnapshotMode: ss.Spec.SnapshotMode,
			TargetSandboxRef: corev1.TypedLocalObjectReference{
				APIGroup: ptr.To("agents.x-k8s.io"),
				Kind:     "Sandbox",
				Name:     buildSandbox.Name,
			},
			TargetNodeName: nodeName,
			ProviderName:   sc.Spec.ProviderName,
			SnapshotKey:    snapshotKey,
			SnapshotHash:   snapshotHash,
		},
	}
	if err := controllerutil.SetControllerReference(ss, task, h.Scheme); err != nil {
		return fmt.Errorf("set controller reference on task: %w", err)
	}
	if err := h.Client.Create(ctx, task); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create snapshot task %s: %w", taskName, err)
	}
	return nil
}

func (h *ForkModeHandler) ensureBuildSandbox(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, snapshotKey, nodeName, sbName string) (*sandboxv1alpha1.Sandbox, error) {
	buildSandbox := &sandboxv1alpha1.Sandbox{}
	err := h.Client.Get(ctx, types.NamespacedName{Name: sbName, Namespace: ss.Namespace}, buildSandbox)
	if apierrors.IsNotFound(err) {
		tmpl := &extensionsv1alpha1.SandboxTemplate{}
		if err := h.Client.Get(ctx, types.NamespacedName{Name: ss.Spec.SourceRef.Name, Namespace: ss.Namespace}, tmpl); err != nil {
			return nil, fmt.Errorf("get source SandboxTemplate: %w", err)
		}
		podSpec := tmpl.Spec.PodTemplate.Spec.DeepCopy()
		podSpec.NodeName = nodeName

		buildSandbox = &sandboxv1alpha1.Sandbox{
			ObjectMeta: metaWithLabels(sbName, ss.Namespace, map[string]string{
				runtimev1alpha1.SnapshotNameLabelKey:  ss.Name,
				runtimev1alpha1.SnapshotKeyLabelKey:   snapshotKey,
				runtimev1alpha1.SnapshotNodeLabelKey:  nodeName,
				runtimev1alpha1.SnapshotBuildLabelKey: "true",
			}),
			Spec: sandboxv1alpha1.SandboxSpec{
				PodTemplate: sandboxv1alpha1.PodTemplate{Spec: *podSpec},
				Replicas:    ptr.To[int32](1),
			},
		}
		if err := controllerutil.SetControllerReference(ss, buildSandbox, h.Scheme); err != nil {
			return nil, fmt.Errorf("set controller reference on build sandbox: %w", err)
		}
		if err := h.Client.Create(ctx, buildSandbox); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create build sandbox %s: %w", sbName, err)
		}
		if err := h.Client.Get(ctx, types.NamespacedName{Name: sbName, Namespace: ss.Namespace}, buildSandbox); err != nil {
			return nil, fmt.Errorf("re-fetch build sandbox: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("get build sandbox %s: %w", sbName, err)
	}
	return buildSandbox, nil
}

// -- Fork-specific helper functions --

func forkRebuildsOnSourceChange(ss *runtimev1alpha1.SandboxSnapshot) bool {
	if ss.Spec.ForkPolicy == nil || ss.Spec.ForkPolicy.RebuildOnSourceChange == nil {
		return true
	}
	return *ss.Spec.ForkPolicy.RebuildOnSourceChange
}

func allNodeArtifactsReady(artifacts []store.SnapshotArtifact) bool {
	if len(artifacts) == 0 {
		return false
	}
	for _, a := range artifacts {
		if a.Phase != store.SnapshotArtifactPhaseReady {
			return false
		}
	}
	return true
}

func coveredArtifactNodes(artifacts []store.SnapshotArtifact) map[string]struct{} {
	covered := make(map[string]struct{}, len(artifacts))
	for _, art := range artifacts {
		covered[art.NodeName] = struct{}{}
	}
	return covered
}

func newCreatingArtifact(providerName, nodeName string, artifactSet store.SnapshotArtifactSet) store.SnapshotArtifact {
	return store.SnapshotArtifact{
		ProviderName: providerName,
		NodeName:     nodeName,
		Phase:        store.SnapshotArtifactPhaseCreating,
		SnapshotKey:  artifactSet.SnapshotKey,
		SnapshotHash: artifactSet.SnapshotHash,
	}
}

func buildSandboxName(snapshotName, nodeName string) string {
	return fmt.Sprintf("%s-build-%s", normalizeLabel(snapshotName), normalizeLabel(nodeName))
}

func buildTaskName(nodeName, snapshotKey string) string {
	return fmt.Sprintf("%s-%s", normalizeLabel(snapshotKey), normalizeLabel(nodeName))
}

func nodeIsReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// snapshotHashInput is the stable serialization for hash computation.
type snapshotHashInput struct {
	SnapshotMode    string            `json:"snapshotMode"`
	SourceNamespace string            `json:"sourceNamespace"`
	SourceName      string            `json:"sourceName"`
	SourceUID       string            `json:"sourceUID"`
	PodTemplateSpec corev1.PodSpec    `json:"podTemplateSpec"`
	SnapshotClass   snapshotClassHash `json:"snapshotClass"`
}

type snapshotClassHash struct {
	Name         string `json:"name"`
	ProviderName string `json:"providerName"`
}

func computeSnapshotHash(ss *runtimev1alpha1.SandboxSnapshot, podSpec corev1.PodSpec, sc *runtimev1alpha1.SnapshotClass) (string, error) {
	input := snapshotHashInput{
		SnapshotMode:    string(ss.Spec.SnapshotMode),
		SourceNamespace: ss.Namespace,
		SourceName:      ss.Spec.SourceRef.Name,
		SourceUID:       string(ss.UID),
		PodTemplateSpec: normalizePodSpec(podSpec),
		SnapshotClass: snapshotClassHash{
			Name:         sc.Name,
			ProviderName: sc.Spec.ProviderName,
		},
	}
	data, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("sha256:%x", h), nil
}

func normalizePodSpec(spec corev1.PodSpec) corev1.PodSpec {
	s := spec.DeepCopy()
	s.NodeName = ""
	sort.Slice(s.Tolerations, func(i, j int) bool {
		return s.Tolerations[i].Key < s.Tolerations[j].Key
	})
	return *s
}

func metaWithLabels(name, namespace string, lbls map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{
		Name:      name,
		Namespace: namespace,
		Labels:    lbls,
	}
}

// lookupActiveForkSnapshotKey returns the active snapshot key for a Fork-mode SandboxSnapshot
// whose sourceRef matches sandboxTemplateName, or an empty string when none is Ready.
// Errors from the artifact store are logged and treated as cache-miss so session creation
// falls back to cold start rather than failing.
func lookupActiveForkSnapshotKey(
	ctx context.Context,
	k8sClient client.Client,
	artifactStore store.ArtifactStore,
	namespace, sandboxTemplateName string,
) string {
	snapshotList := &runtimev1alpha1.SandboxSnapshotList{}
	if err := k8sClient.List(ctx, snapshotList, client.InNamespace(namespace)); err != nil {
		klog.V(4).InfoS("snapshot lookup: failed to list snapshots, falling back to cold start",
			"namespace", namespace, "error", err)
		return ""
	}

	for i := range snapshotList.Items {
		ss := &snapshotList.Items[i]
		if ss.Spec.SnapshotMode != runtimev1alpha1.SandboxSnapshotModeFork {
			continue
		}
		if ss.Spec.SourceRef.Name != sandboxTemplateName {
			continue
		}
		if ss.Status.Phase != runtimev1alpha1.SandboxSnapshotPhaseReady {
			continue
		}

		ownerKey := store.ArtifactOwnerKey("SandboxSnapshot", ss.Namespace, ss.Name, string(ss.UID))
		manifest, err := artifactStore.GetManifest(ctx, ownerKey)
		if err != nil {
			klog.V(4).InfoS("snapshot lookup: artifact store error, falling back to cold start",
				"snapshot", ss.Name, "error", err)
			continue
		}
		if manifest == nil || manifest.ActiveSetRef.SnapshotKey == "" {
			continue
		}
		activeSet, ok := manifest.ArtifactSets[manifest.ActiveSetRef.SnapshotKey]
		if !ok {
			continue
		}
		for _, art := range activeSet.Artifacts {
			if art.Phase == store.SnapshotArtifactPhaseReady {
				klog.V(4).InfoS("snapshot lookup: found active fork snapshot key",
					"snapshot", ss.Name, "snapshotKey", manifest.ActiveSetRef.SnapshotKey)
				return manifest.ActiveSetRef.SnapshotKey
			}
		}
	}
	return ""
}
