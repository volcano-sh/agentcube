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
	stderrors "errors"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	// gcOnceTimeout caps the wall-clock time a single GC run is allowed to take.
	// Unrelated to gcMinInactiveLookback — the 2m vs 1m coincidence is not meaningful.
	gcOnceTimeout = 2 * time.Minute
	// gcMinInactiveLookback is the minimum time a sandbox must have been idle before
	// it is included in the GC's inactive candidate query. Sandboxes with an IdleTimeout
	// shorter than this window are not caught by this check; those rely on the
	// idle-timeout annotation enforced by the agent-sandbox controller.
	gcMinInactiveLookback = 1 * time.Minute
	// gcCandidateLimit caps the expiry batch and is the inactive scan work hint.
	// ZSCAN can return more or fewer entries, including empty nonterminal pages.
	gcCandidateLimit = 100
)

type garbageCollector struct {
	k8sClient   *K8sClient
	interval    time.Duration
	storeClient store.Store

	// Only the serial GC loop accesses this state. Keep unprocessed IDs, not
	// sandbox snapshots: sessions may become active before the next GC run.
	inactiveCursor  uint64
	inactivePending []string
}

func newGarbageCollector(k8sClient *K8sClient, storeClient store.Store, interval time.Duration) *garbageCollector {
	return &garbageCollector{
		k8sClient:   k8sClient,
		interval:    interval,
		storeClient: storeClient,
	}
}

func (gc *garbageCollector) run(stopCh <-chan struct{}) {
	ticker := time.NewTicker(gc.interval)
	for {
		select {
		case <-stopCh:
			ticker.Stop()
			klog.Info("garbage collector stopped")
			return
		case <-ticker.C:
			gc.once()
		}
	}
}

func (gc *garbageCollector) once() {
	ctx, cancel := context.WithTimeout(context.Background(), gcOnceTimeout)
	defer cancel()
	gc.collect(ctx, time.Now())
}

// collect shares a run deadline while reserving time for inactive traversal.
func (gc *garbageCollector) collect(ctx context.Context, now time.Time) {
	seen := make(map[string]struct{})
	expiryBudget := gcOnceTimeout / 2
	if deadline, ok := ctx.Deadline(); ok {
		expiryBudget = min(expiryBudget, time.Until(deadline)/2)
	}
	expiryCtx, cancel := context.WithTimeout(ctx, expiryBudget)
	gc.collectExpired(expiryCtx, now, seen)
	cancel()
	gc.collectInactive(ctx, now, seen)
}

func (gc *garbageCollector) collectExpired(ctx context.Context, now time.Time, seen map[string]struct{}) {
	if ctx.Err() != nil {
		return
	}
	sandboxes, err := gc.storeClient.ListExpiredSandboxes(ctx, now, gcCandidateLimit)
	if err != nil {
		klog.Errorf("garbage collector error listing expired sandboxes: %v", err)
		return
	}
	for _, sandbox := range sandboxes {
		if ctx.Err() != nil {
			return
		}
		if err := gc.deleteCandidate(ctx, sandbox, seen); err != nil {
			klog.Errorf("garbage collector error deleting expired session %s: %v", sandbox.SessionID, err)
		}
	}
}

func (gc *garbageCollector) collectInactive(ctx context.Context, now time.Time, seen map[string]struct{}) {
	for ctx.Err() == nil {
		if len(gc.inactivePending) == 0 {
			ids, next, err := gc.storeClient.ScanInactiveSandboxIDs(ctx, now.Add(-gcMinInactiveLookback), gc.inactiveCursor, gcCandidateLimit)
			if err != nil {
				klog.Errorf("garbage collector error scanning inactive sandboxes: %v", err)
				return
			}
			gc.inactivePending, gc.inactiveCursor = ids, next
		}
		for len(gc.inactivePending) > 0 {
			if ctx.Err() != nil {
				return
			}
			id := gc.inactivePending[0]
			err := gc.collectInactiveSession(ctx, id, now, seen)
			// Retain the interrupted ID even if an operation partially succeeded.
			// Deletes are idempotent and the next run will refresh the session.
			if ctx.Err() != nil {
				return
			}
			if err != nil && !stderrors.Is(err, store.ErrNotFound) {
				klog.Errorf("garbage collector error processing inactive session %s: %v", id, err)
			}
			// Ordinary per-session failures must not block the rest of the sweep.
			// They will be retried when a later sweep encounters the session.
			gc.inactivePending[0] = ""
			gc.inactivePending = gc.inactivePending[1:]
		}
		gc.inactivePending = nil
		if gc.inactiveCursor == 0 {
			return
		}
	}
}

func (gc *garbageCollector) collectInactiveSession(ctx context.Context, id string, now time.Time, seen map[string]struct{}) error {
	if _, ok := seen[id]; ok {
		return nil
	}
	sandbox, err := gc.storeClient.GetSandboxWithLastActivity(ctx, id)
	if err != nil {
		return err
	}
	activityAt := sandbox.LastActivityAt
	if activityAt.IsZero() {
		klog.Warningf("garbage collector: no last-activity for sandbox %s/%s (session %s), falling back to CreatedAt", sandbox.SandboxNamespace, sandbox.Name, sandbox.SessionID)
		activityAt = sandbox.CreatedAt
	}
	// The scan may have happened during an earlier run. Recheck its cutoff with
	// the fresh score before applying the individual idle timeout.
	if activityAt.Unix() > now.Add(-gcMinInactiveLookback).Unix() {
		return nil
	}
	idleTimeout := sandbox.IdleTimeout.Duration
	if idleTimeout == 0 {
		idleTimeout = DefaultSandboxIdleTimeout
	}
	if !activityAt.Add(idleTimeout).Before(now) {
		return nil
	}
	return gc.deleteCandidate(ctx, sandbox, seen)
}

func (gc *garbageCollector) deleteCandidate(ctx context.Context, sandbox *types.SandboxInfo, seen map[string]struct{}) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, ok := seen[sandbox.SessionID]; ok {
		return nil
	}
	seen[sandbox.SessionID] = struct{}{}
	var err error
	if sandbox.Kind == types.SandboxClaimsKind {
		err = gc.deleteSandboxClaim(ctx, sandbox.SandboxNamespace, sandbox.Name)
	} else {
		err = gc.deleteSandbox(ctx, sandbox.SandboxNamespace, sandbox.Name)
	}
	if err != nil {
		return err
	}
	klog.Infof("garbage collector %s %s/%s session %s deleted", sandbox.Kind, sandbox.SandboxNamespace, sandbox.Name, sandbox.SessionID)
	if err := ctx.Err(); err != nil {
		return err
	}
	return gc.storeClient.DeleteSandboxBySessionID(ctx, sandbox.SessionID)
}

func (gc *garbageCollector) deleteSandbox(ctx context.Context, namespace, name string) error {
	err := gc.k8sClient.dynamicClient.Resource(SandboxGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting sandbox %s/%s: %w", namespace, name, err)
	}
	return nil
}

func (gc *garbageCollector) deleteSandboxClaim(ctx context.Context, namespace, name string) error {
	err := gc.k8sClient.dynamicClient.Resource(SandboxClaimGVR).Namespace(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting sandboxClaim %s/%s: %w", namespace, name, err)
	}
	return nil
}
