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
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/klog/v2"

	"github.com/volcano-sh/agentcube/pkg/common/types"
	"github.com/volcano-sh/agentcube/pkg/store"
)

const (
	gcOnceTimeout = 2 * time.Minute
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

	// Query candidates inactive for at least DefaultSandboxIdleTimeout. This is the
	// minimum configured idle timeout; sandboxes with a longer per-sandbox IdleTimeout
	// are filtered below so they are not deleted prematurely.
	candidates, err := gc.storeClient.ListInactiveSandboxes(ctx, now.Add(-DefaultSandboxIdleTimeout), 16)
	if err != nil {
		klog.Errorf("garbage collector error listing inactive sandboxes: %v", err)
	}

	// Apply per-sandbox idle timeout: only include sandboxes whose own IdleTimeout
	// (stored in the session JSON) has actually elapsed since LastActivityAt.
	inactiveSandboxes := make([]*types.SandboxInfo, 0, len(candidates))
	for _, s := range candidates {
		idleTimeout := s.IdleTimeout
		if idleTimeout == 0 {
			idleTimeout = DefaultSandboxIdleTimeout
		}
		if s.LastActivityAt.Add(idleTimeout).Before(now) {
			inactiveSandboxes = append(inactiveSandboxes, s)
		}
	}

	// List sandboxes reach DDL
	expiredSandboxes, err := gc.storeClient.ListExpiredSandboxes(ctx, now, 16)
	if err != nil {
		klog.Errorf("garbage collector error listing expired sandboxes: %v", err)
	}
	gcSandboxes := make([]*types.SandboxInfo, 0, len(inactiveSandboxes)+len(expiredSandboxes))
	gcSandboxes = append(gcSandboxes, inactiveSandboxes...)
	gcSandboxes = append(gcSandboxes, expiredSandboxes...)

	if len(gcSandboxes) > 0 {
		klog.Infof("garbage collector found %d sandboxes to be deleted", len(gcSandboxes))
	}

	errs := make([]error, 0, len(gcSandboxes))
	// delete sandboxes
	for _, gcSandbox := range gcSandboxes {
		if gcSandbox.Kind == types.SandboxClaimsKind {
			err = gc.deleteSandboxClaim(ctx, gcSandbox.SandboxNamespace, gcSandbox.Name)
		} else {
			err = gc.deleteSandbox(ctx, gcSandbox.SandboxNamespace, gcSandbox.Name)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		klog.Infof("garbage collector %s %s/%s session %s deleted", gcSandbox.Kind, gcSandbox.SandboxNamespace, gcSandbox.Name, gcSandbox.SessionID)
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
