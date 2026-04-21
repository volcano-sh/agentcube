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
	"testing"
	"time"

	"k8s.io/client-go/tools/cache"
)

// neverSyncedInformer is a cache.SharedIndexInformer whose HasSynced always returns false.
type neverSyncedInformer struct {
	cache.SharedIndexInformer
}

func (n *neverSyncedInformer) HasSynced() bool { return false }

func TestRunAndWaitForCacheSync_RespectsContextCancellation(t *testing.T) {
	ifm := &Informers{
		AgentRuntimeInformer:    &neverSyncedInformer{},
		CodeInterpreterInformer: &neverSyncedInformer{},
		PodInformer:             &neverSyncedInformer{},
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		// waitForCacheSync calls run internally via RunAndWaitForCacheSync,
		// but run needs a real informerFactory. Call waitForCacheSync directly
		// to isolate the context-propagation behaviour.
		ctxTimeout, cancelTimeout := context.WithTimeout(ctx, 1*time.Minute)
		defer cancelTimeout()
		done <- ifm.waitForCacheSync(ctxTimeout)
	}()

	// Cancel the parent context well before the 1-minute timeout.
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected error when context is cancelled, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waitForCacheSync did not respect context cancellation within 2s")
	}
}
