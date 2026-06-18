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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sandboxv1alpha1 "sigs.k8s.io/agent-sandbox/api/v1alpha1"

	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	snapshotFinalizer = "agentcube.volcano.sh/snapshot-protection"
	maxInt32Value     = 1<<31 - 1
)

// SandboxSnapshotReconciler reconciles SandboxSnapshot objects.
// Mode-specific logic is fully delegated to the registered SnapshotModeHandler.
type SandboxSnapshotReconciler struct {
	client.Client
	ArtifactStore store.ArtifactStore
	Recorder      record.EventRecorder
	Handlers      map[runtimev1alpha1.SandboxSnapshotMode]SnapshotModeHandler
}

func (r *SandboxSnapshotReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	ss := &runtimev1alpha1.SandboxSnapshot{}
	if err := r.Get(ctx, req.NamespacedName, ss); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !ss.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, ss)
	}

	if !controllerutil.ContainsFinalizer(ss, snapshotFinalizer) {
		controllerutil.AddFinalizer(ss, snapshotFinalizer)
		if err := r.Update(ctx, ss); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	sc := &runtimev1alpha1.SnapshotClass{}
	if err := r.Get(ctx, types.NamespacedName{Name: ss.Spec.SnapshotClassName}, sc); err != nil {
		if apierrors.IsNotFound(err) {
			return r.setFailed(ctx, ss, "SnapshotClass not found: "+ss.Spec.SnapshotClassName)
		}
		return ctrl.Result{}, err
	}

	handler, ok := r.Handlers[ss.Spec.SnapshotMode]
	if !ok {
		return r.setFailed(ctx, ss, "unsupported snapshotMode: "+string(ss.Spec.SnapshotMode))
	}
	if !containsMode(sc.Spec.SupportedSnapshotModes, ss.Spec.SnapshotMode) {
		return r.setFailed(ctx, ss, fmt.Sprintf("SnapshotClass %q does not support mode %q", sc.Name, ss.Spec.SnapshotMode))
	}

	return r.reconcileWithHandler(ctx, ss, sc, handler)
}

func (r *SandboxSnapshotReconciler) reconcileWithHandler(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass, handler SnapshotModeHandler) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	currentHash, err := handler.ComputeHash(ctx, ss, sc)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("compute snapshot hash: %w", err)
	}

	ownerKey := store.ArtifactOwnerKey("SandboxSnapshot", ss.Namespace, ss.Name, string(ss.UID))
	manifest, rawVersion, err := loadManifest(ctx, r.ArtifactStore, ownerKey)
	if err != nil {
		return ctrl.Result{}, err
	}

	rawVersion, err = handler.PrepareArtifactSet(ctx, ss, sc, manifest, ownerKey, rawVersion, currentHash)
	if err != nil {
		return ctrl.Result{}, err
	}

	rawVersion, err = r.reconcileTasksAndArtifacts(ctx, ss, sc, manifest, ownerKey, rawVersion, handler)
	if err != nil {
		return ctrl.Result{}, err
	}

	if manifest.PendingSetRef.SnapshotKey != "" {
		pending := manifest.ArtifactSets[manifest.PendingSetRef.SnapshotKey]
		if handler.ReadyToPromote(pending) {
			logger.Info("promoting pending artifact set to active", "snapshot", ss.Name, "snapshotKey", pending.SnapshotKey)
			if manifest.ActiveSetRef.SnapshotKey != "" {
				delete(manifest.ArtifactSets, manifest.ActiveSetRef.SnapshotKey)
			}
			manifest.ActiveSetRef = manifest.PendingSetRef
			manifest.PendingSetRef = store.SnapshotArtifactSetRef{}
			ss.Status.ReadyAt = nil
			if _, err = saveManifest(ctx, r.ArtifactStore, ownerKey, manifest, rawVersion); err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	return r.aggregateAndUpdateStatus(ctx, ss, manifest)
}

func (r *SandboxSnapshotReconciler) reconcileDelete(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ownerKey := store.ArtifactOwnerKey("SandboxSnapshot", ss.Namespace, ss.Name, string(ss.UID))
	if err := r.ArtifactStore.DeleteManifest(ctx, ownerKey); err != nil {
		return ctrl.Result{}, fmt.Errorf("delete artifact manifest: %w", err)
	}
	logger.Info("deleted artifact manifest", "snapshot", ss.Name)

	if handler, ok := r.Handlers[ss.Spec.SnapshotMode]; ok {
		if err := handler.CleanupAll(ctx, ss); err != nil {
			return ctrl.Result{}, err
		}
	}

	controllerutil.RemoveFinalizer(ss, snapshotFinalizer)
	return ctrl.Result{}, r.Update(ctx, ss)
}

func (r *SandboxSnapshotReconciler) reconcileTasksAndArtifacts(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, sc *runtimev1alpha1.SnapshotClass, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion string, handler SnapshotModeHandler) (string, error) {
	workingKey := manifest.PendingSetRef.SnapshotKey
	if workingKey == "" {
		workingKey = manifest.ActiveSetRef.SnapshotKey
	}
	if workingKey == "" {
		return rawVersion, nil
	}
	artifactSet, exists := manifest.ArtifactSets[workingKey]
	if !exists {
		return rawVersion, nil
	}

	taskList, err := r.listSnapshotTasks(ctx, ss, workingKey)
	if err != nil {
		return rawVersion, err
	}
	rawVersion, artifactSet, err = r.syncArtifactStatus(ctx, manifest, ownerKey, rawVersion, workingKey, artifactSet, taskList)
	if err != nil {
		return rawVersion, err
	}

	rawVersion, err = handler.EnsureTasks(ctx, ss, sc, manifest, ownerKey, rawVersion, workingKey, artifactSet)
	if err != nil {
		return rawVersion, err
	}

	if workingKey == manifest.ActiveSetRef.SnapshotKey {
		r.cleanupCompletedTasks(ctx, ss, workingKey, handler)
	}

	return rawVersion, nil
}

func (r *SandboxSnapshotReconciler) cleanupCompletedTasks(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, snapshotKey string, handler SnapshotModeHandler) {
	logger := log.FromContext(ctx)

	taskList := &runtimev1alpha1.SandboxSnapshotTaskList{}
	if err := r.List(ctx, taskList, client.InNamespace(ss.Namespace), client.MatchingLabels{
		runtimev1alpha1.SnapshotNameLabelKey: ss.Name,
		runtimev1alpha1.SnapshotKeyLabelKey:  snapshotKey,
	}); err != nil {
		logger.Error(err, "list completed tasks for cleanup")
		return
	}

	for i := range taskList.Items {
		task := &taskList.Items[i]
		phase := task.Status.Phase
		if phase != runtimev1alpha1.SnapshotArtifactPhaseReady && phase != runtimev1alpha1.SnapshotArtifactPhaseFailed {
			continue
		}
		if err := handler.CleanupTask(ctx, ss, task); err != nil {
			logger.Error(err, "mode-specific task cleanup failed", "task", task.Name)
		}
		if err := r.Delete(ctx, task); err != nil && !apierrors.IsNotFound(err) {
			logger.Error(err, "delete completed task", "name", task.Name)
		}
	}
}

func (r *SandboxSnapshotReconciler) listSnapshotTasks(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, snapshotKey string) (*runtimev1alpha1.SandboxSnapshotTaskList, error) {
	taskList := &runtimev1alpha1.SandboxSnapshotTaskList{}
	if err := r.List(ctx, taskList, client.InNamespace(ss.Namespace), client.MatchingLabels{
		runtimev1alpha1.SnapshotNameLabelKey: ss.Name,
		runtimev1alpha1.SnapshotKeyLabelKey:  snapshotKey,
	}); err != nil {
		return nil, fmt.Errorf("list snapshot tasks: %w", err)
	}
	return taskList, nil
}

func (r *SandboxSnapshotReconciler) syncArtifactStatus(ctx context.Context, manifest *store.SnapshotArtifactManifest, ownerKey, rawVersion, workingKey string, artifactSet store.SnapshotArtifactSet, taskList *runtimev1alpha1.SandboxSnapshotTaskList) (string, store.SnapshotArtifactSet, error) {
	tasksByNode := make(map[string]*runtimev1alpha1.SandboxSnapshotTask, len(taskList.Items))
	for i := range taskList.Items {
		t := &taskList.Items[i]
		tasksByNode[t.Spec.TargetNodeName] = t
	}

	changed := false
	for i := range artifactSet.Artifacts {
		art := &artifactSet.Artifacts[i]
		task, hasTask := tasksByNode[art.NodeName]
		if !hasTask {
			continue
		}
		if updateArtifactFromTask(art, task) {
			changed = true
		}
	}
	if !changed {
		return rawVersion, artifactSet, nil
	}
	manifest.ArtifactSets[workingKey] = artifactSet
	rawVersion, err := saveManifest(ctx, r.ArtifactStore, ownerKey, manifest, rawVersion)
	return rawVersion, artifactSet, err
}

func updateArtifactFromTask(art *store.SnapshotArtifact, task *runtimev1alpha1.SandboxSnapshotTask) bool {
	newPhase := store.SnapshotArtifactPhase(task.Status.Phase)
	if newPhase == "" || newPhase == art.Phase {
		return false
	}
	art.Phase = newPhase
	art.Message = task.Status.Message
	if newPhase == store.SnapshotArtifactPhaseReady && art.CreatedAt == nil {
		now := time.Now()
		art.CreatedAt = &now
	}
	return true
}

func (r *SandboxSnapshotReconciler) aggregateAndUpdateStatus(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, manifest *store.SnapshotArtifactManifest) (ctrl.Result, error) {
	activeSet := activeArtifactSet(manifest)
	pendingSet := pendingArtifactSet(manifest)
	status := r.buildSnapshotStatus(ss, activeSet, pendingSet)

	if status.Phase == runtimev1alpha1.SandboxSnapshotPhaseReady && ss.Status.Phase != runtimev1alpha1.SandboxSnapshotPhaseReady {
		r.Recorder.Event(ss, corev1.EventTypeNormal, "SandboxSnapshotReady", "at least one artifact is available")
	}
	if status.Phase == runtimev1alpha1.SandboxSnapshotPhaseCreating {
		return r.patchSnapshotStatus(ctx, ss, status, ctrl.Result{RequeueAfter: 15 * time.Second})
	}
	return r.patchSnapshotStatus(ctx, ss, status, ctrl.Result{})
}

func (r *SandboxSnapshotReconciler) buildSnapshotStatus(ss *runtimev1alpha1.SandboxSnapshot, activeSet, pendingSet *store.SnapshotArtifactSet) runtimev1alpha1.SandboxSnapshotStatus {
	var workingArtifacts []store.SnapshotArtifact
	if activeSet != nil {
		workingArtifacts = activeSet.Artifacts
	} else if pendingSet != nil {
		workingArtifacts = pendingSet.Artifacts
	}

	status := runtimev1alpha1.SandboxSnapshotStatus{
		TargetNodeCount: int32Len(workingArtifacts),
	}
	countArtifactPhases(workingArtifacts, &status)

	total := status.TargetNodeCount
	switch {
	case total == 0:
		status.Phase = runtimev1alpha1.SandboxSnapshotPhasePending
	case status.ReadyNodeCount > 0 && activeSet != nil:
		status.Phase = runtimev1alpha1.SandboxSnapshotPhaseReady
		if ss.Status.ReadyAt == nil {
			now := metav1.Now()
			status.ReadyAt = &now
		} else {
			status.ReadyAt = ss.Status.ReadyAt
		}
		if status.FailedNodeCount > 0 || status.UnavailableNodeCount > 0 {
			r.Recorder.Event(ss, corev1.EventTypeWarning, "SandboxSnapshotDegraded",
				fmt.Sprintf("%d/%d nodes have failed or unavailable artifacts", status.FailedNodeCount+status.UnavailableNodeCount, total))
		}
	case status.FailedNodeCount == total && total > 0:
		status.Phase = runtimev1alpha1.SandboxSnapshotPhaseFailed
		r.Recorder.Event(ss, corev1.EventTypeWarning, "SandboxSnapshotFailed", "all artifact builds failed")
	default:
		status.Phase = runtimev1alpha1.SandboxSnapshotPhaseCreating
	}
	return status
}

func (r *SandboxSnapshotReconciler) patchSnapshotStatus(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, status runtimev1alpha1.SandboxSnapshotStatus, result ctrl.Result) (ctrl.Result, error) {
	if snapshotStatusEqual(ss.Status, status) {
		return result, nil
	}
	patch := client.MergeFrom(ss.DeepCopy())
	ss.Status = status
	if err := r.Status().Patch(ctx, ss, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("patch snapshot status: %w", err)
	}
	return result, nil
}

func (r *SandboxSnapshotReconciler) setFailed(ctx context.Context, ss *runtimev1alpha1.SandboxSnapshot, msg string) (ctrl.Result, error) {
	patch := client.MergeFrom(ss.DeepCopy())
	ss.Status.Phase = runtimev1alpha1.SandboxSnapshotPhaseFailed
	ss.Status.Message = msg
	if err := r.Status().Patch(ctx, ss, patch); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// -- Shared manifest helpers --

func loadManifest(ctx context.Context, as store.ArtifactStore, ownerKey string) (*store.SnapshotArtifactManifest, string, error) {
	manifest, err := as.GetManifest(ctx, ownerKey)
	if err != nil {
		return nil, "", fmt.Errorf("get artifact manifest: %w", err)
	}
	if manifest == nil {
		return &store.SnapshotArtifactManifest{}, "", nil
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, "", fmt.Errorf("marshal manifest for version: %w", err)
	}
	return manifest, string(raw), nil
}

func saveManifest(ctx context.Context, as store.ArtifactStore, ownerKey string, manifest *store.SnapshotArtifactManifest, version string) (string, error) {
	if err := as.PutManifest(ctx, ownerKey, manifest, version); err != nil {
		if errors.Is(err, store.ErrArtifactStoreConflict) {
			return version, fmt.Errorf("artifact store conflict (will retry): %w", err)
		}
		return version, fmt.Errorf("put artifact manifest: %w", err)
	}
	raw, _ := json.Marshal(manifest)
	return string(raw), nil
}

func startNewArtifactSet(ss *runtimev1alpha1.SandboxSnapshot, manifest *store.SnapshotArtifactManifest, snapshotHash string) string {
	manifest.RebuildSeq++
	snapshotKey := buildSnapshotKey(ss, manifest.RebuildSeq)
	if manifest.ArtifactSets == nil {
		manifest.ArtifactSets = make(map[string]store.SnapshotArtifactSet)
	}
	manifest.ArtifactSets[snapshotKey] = store.SnapshotArtifactSet{
		SnapshotKey:  snapshotKey,
		SnapshotHash: snapshotHash,
	}
	return snapshotKey
}

// -- Shared key/label utilities --

func buildSnapshotKey(ss *runtimev1alpha1.SandboxSnapshot, rebuildSeq int32) string {
	mode := strings.ToLower(string(ss.Spec.SnapshotMode))
	return fmt.Sprintf("%s-%s-g%d-r%d", normalizeLabel(ss.Name), mode, ss.Generation, rebuildSeq)
}

func normalizeLabel(s string) string {
	var b strings.Builder
	for _, c := range strings.ToLower(s) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			b.WriteRune(c)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-")
	if len(result) > 63 {
		result = result[:63]
	}
	return result
}

// -- Shared manifest accessors --

func activeArtifactSet(manifest *store.SnapshotArtifactManifest) *store.SnapshotArtifactSet {
	if manifest.ActiveSetRef.SnapshotKey == "" {
		return nil
	}
	s, ok := manifest.ArtifactSets[manifest.ActiveSetRef.SnapshotKey]
	if !ok {
		return nil
	}
	return &s
}

func pendingArtifactSet(manifest *store.SnapshotArtifactManifest) *store.SnapshotArtifactSet {
	if manifest.PendingSetRef.SnapshotKey == "" {
		return nil
	}
	s, ok := manifest.ArtifactSets[manifest.PendingSetRef.SnapshotKey]
	if !ok {
		return nil
	}
	return &s
}

// -- Shared status helpers --

func snapshotStatusEqual(a, b runtimev1alpha1.SandboxSnapshotStatus) bool {
	return a.Phase == b.Phase &&
		a.TargetNodeCount == b.TargetNodeCount &&
		a.CreatingNodeCount == b.CreatingNodeCount &&
		a.ReadyNodeCount == b.ReadyNodeCount &&
		a.FailedNodeCount == b.FailedNodeCount &&
		a.UnavailableNodeCount == b.UnavailableNodeCount &&
		a.Message == b.Message
}

func countArtifactPhases(artifacts []store.SnapshotArtifact, status *runtimev1alpha1.SandboxSnapshotStatus) {
	for _, art := range artifacts {
		switch art.Phase {
		case store.SnapshotArtifactPhaseCreating:
			status.CreatingNodeCount++
		case store.SnapshotArtifactPhaseReady:
			status.ReadyNodeCount++
		case store.SnapshotArtifactPhaseFailed:
			status.FailedNodeCount++
		case store.SnapshotArtifactPhaseUnavailable:
			status.UnavailableNodeCount++
		}
	}
}

func int32Len(artifacts []store.SnapshotArtifact) int32 {
	count := int32(0)
	for range artifacts {
		if count == maxInt32Value {
			return maxInt32Value
		}
		count++
	}
	return count
}

func containsMode(modes []runtimev1alpha1.SandboxSnapshotMode, mode runtimev1alpha1.SandboxSnapshotMode) bool {
	for _, m := range modes {
		if m == mode {
			return true
		}
	}
	return false
}

// SetupWithManager registers the controller and initializes mode handlers.
func (r *SandboxSnapshotReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("sandbox-snapshot-controller")
	r.Handlers = map[runtimev1alpha1.SandboxSnapshotMode]SnapshotModeHandler{
		runtimev1alpha1.SandboxSnapshotModeFork: &ForkModeHandler{
			Client:        r.Client,
			ArtifactStore: r.ArtifactStore,
			Recorder:      r.Recorder,
			Scheme:        mgr.GetScheme(),
		},
	}
	return ctrl.NewControllerManagedBy(mgr).
		For(&runtimev1alpha1.SandboxSnapshot{}).
		Owns(&runtimev1alpha1.SandboxSnapshotTask{}).
		Owns(&sandboxv1alpha1.Sandbox{}).
		Complete(r)
}
