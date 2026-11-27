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
	"time"

	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	// gcOnceTimeout caps the wall-clock time a single GC run is allowed to take.
	gcOnceTimeout = 2 * time.Minute
	// gcCandidateLimit caps each query's batch of eligible sandboxes.
	gcCandidateLimit = 100
)

type garbageCollector struct {
	k8sClient   *K8sClient
	interval    time.Duration
	storeClient store.Store
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
	now := time.Now()

	// The index is ordered by idle deadline, so all returned sessions are eligible.
	// Long idle timeouts cannot hide expired deadlines behind the batch limit.
	inactiveSandboxes, err := gc.storeClient.ListInactiveSandboxes(ctx, now, gcCandidateLimit)
	if err != nil {
		klog.Errorf("garbage collector error listing inactive sandboxes: %v", err)
	}

	// List sandboxes that have reached their expiry deadline
	expiredSandboxes, err := gc.storeClient.ListExpiredSandboxes(ctx, now, gcCandidateLimit)
	if err != nil {
		klog.Errorf("garbage collector error listing expired sandboxes: %v", err)
	}
	// Merge and deduplicate: a sandbox may appear in both lists when it is
	// simultaneously idle-timed-out and past its TTL.
	gcSandboxes := deduplicateSandboxes(inactiveSandboxes, expiredSandboxes)

	if len(gcSandboxes) > 0 {
		klog.Infof("garbage collector found %d sandboxes to be deleted", len(gcSandboxes))
	}

	errs := make([]error, 0, len(gcSandboxes))
	// delete sandboxes
	for _, gcSandbox := range gcSandboxes {
		if gcSandbox.Kind == types.SandboxClaimsKind {
			err = deleteSandboxClaim(ctx, gc.k8sClient.dynamicClient, gcSandbox.SandboxNamespace, gcSandbox.Name)
		} else {
			err = deleteSandbox(ctx, gc.k8sClient.dynamicClient, gcSandbox.SandboxNamespace, gcSandbox.Name)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		klog.Infof("garbage collector %s %s/%s session %s deleted", gcSandbox.Kind, gcSandbox.SandboxNamespace, gcSandbox.Name, gcSandbox.SessionID)
		sandboxAuditEvent(ctx, gc.k8sClient.clientset, gcSandbox.SandboxNamespace, gcSandbox.Name,
			sha256Short(gcSandbox.OwnerID), AuditReasonDeleted,
			fmt.Sprintf("sandbox %s/%s GC-deleted (session %s)", gcSandbox.SandboxNamespace, gcSandbox.Name, gcSandbox.SessionID))
		err = gc.storeClient.DeleteSandboxBySessionID(ctx, gcSandbox.SessionID)
		if err != nil {
			errs = append(errs, err)
		}
	}
	err = utilerrors.NewAggregate(errs)
	if err != nil {
		klog.Errorf("garbage collector failed with error: %v", err)
	}
}

// deduplicateSandboxes merges multiple sandbox lists and removes duplicates
// by SessionID, preserving the order of first occurrence.
func deduplicateSandboxes(lists ...[]*types.SandboxInfo) []*types.SandboxInfo {
	total := 0
	for _, l := range lists {
		total += len(l)
	}
	seen := make(map[string]struct{}, total)
	result := make([]*types.SandboxInfo, 0, total)
	for _, list := range lists {
		for _, s := range list {
			if _, ok := seen[s.SessionID]; !ok {
				seen[s.SessionID] = struct{}{}
				result = append(result, s)
			}
		}
	}
	return result
}
