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
	"errors"
	"testing"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	agentcubeinformers "github.com/volcano-sh/agentcube/client-go/informers/externalversions"
	agentruntimev1alpha1 "github.com/volcano-sh/agentcube/client-go/informers/externalversions/runtime/v1alpha1"
	runtimelister "github.com/volcano-sh/agentcube/client-go/listers/runtime/v1alpha1"
)

type neverSyncedInformer struct {
	cache.SharedIndexInformer
}

func (n *neverSyncedInformer) HasSynced() bool                       { return false }
func (n *neverSyncedInformer) Run(stopCh <-chan struct{})             { <-stopCh }
func (n *neverSyncedInformer) Informer() cache.SharedIndexInformer    { return n }
func (n *neverSyncedInformer) Lister() runtimelister.AgentRuntimeLister { return nil }

// fakeCodeInterpreterInformer implements CodeInterpreterInformer
type fakeCodeInterpreterInformer struct {
	cache.SharedIndexInformer
}

func (f *fakeCodeInterpreterInformer) HasSynced() bool                         { return f.SharedIndexInformer.HasSynced() }
func (f *fakeCodeInterpreterInformer) Run(stopCh <-chan struct{})               { f.SharedIndexInformer.Run(stopCh) }
func (f *fakeCodeInterpreterInformer) Informer() cache.SharedIndexInformer      { return f.SharedIndexInformer }
func (f *fakeCodeInterpreterInformer) Lister() runtimelister.CodeInterpreterLister { return nil }

// alwaysSyncedInformer is a cache.SharedIndexInformer whose HasSynced always returns true.
type alwaysSyncedInformer struct {
	cache.SharedIndexInformer
}

func (a *alwaysSyncedInformer) HasSynced() bool                       { return true }
func (a *alwaysSyncedInformer) Run(stopCh <-chan struct{})             { <-stopCh }
func (a *alwaysSyncedInformer) Informer() cache.SharedIndexInformer    { return a }
func (a *alwaysSyncedInformer) Lister() runtimelister.AgentRuntimeLister { return nil }

// runCanceled starts RunAndWaitForCacheSync in a goroutine, cancels the context
// immediately, and returns the error. Fails the test if it takes more than 2s.
func runCanceled(t *testing.T, ifm *Informers) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ifm.RunAndWaitForCacheSync(ctx) }()
	cancel()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("RunAndWaitForCacheSync did not respect context cancellation within 2s")
		return nil
	}
}

func newFactory() (informers.SharedInformerFactory, agentcubeinformers.SharedInformerFactory) {
	return informers.NewSharedInformerFactory(fake.NewSimpleClientset(), 0),
		agentcubeinformers.NewSharedInformerFactory(nil, 0)
}

func TestRunAndWaitForCacheSync_ContextCancellation(t *testing.T) {
	never := func() *neverSyncedInformer { return &neverSyncedInformer{} }
	always := func() *alwaysSyncedInformer { return &alwaysSyncedInformer{} }

	tests := []struct {
		name            string
		agentRuntime    agentruntimev1alpha1.AgentRuntimeInformer
		codeInterpreter agentruntimev1alpha1.CodeInterpreterInformer
		pod             cache.SharedIndexInformer
	}{
		{
			name:            "AgentRuntimeInformer never syncs",
			agentRuntime:    never(),
			codeInterpreter: &fakeCodeInterpreterInformer{SharedIndexInformer: never()},
			pod:             never(),
		},
		{
			name:            "CodeInterpreterInformer never syncs",
			agentRuntime:    always(),
			codeInterpreter: &fakeCodeInterpreterInformer{SharedIndexInformer: never()},
			pod:             never(),
		},
		{
			name:            "PodInformer never syncs",
			agentRuntime:    always(),
			codeInterpreter: &fakeCodeInterpreterInformer{SharedIndexInformer: always()},
			pod:             never(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			factory, agentcubeFactory := newFactory()
			ifm := &Informers{
				AgentRuntimeInformer:    tc.agentRuntime,
				CodeInterpreterInformer: tc.codeInterpreter,
				PodInformer:             tc.pod,
				informerFactory:         factory,
				agentcubeInformer:       agentcubeFactory,
			}
			err := runCanceled(t, ifm)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled, got %v", err)
			}
		})
	}
}

func TestRunAndWaitForCacheSync_AllSynced(t *testing.T) {
	factory, agentcubeFactory := newFactory()
	ifm := &Informers{
		AgentRuntimeInformer:    &alwaysSyncedInformer{},
		CodeInterpreterInformer: &fakeCodeInterpreterInformer{SharedIndexInformer: &alwaysSyncedInformer{}},
		PodInformer:             &alwaysSyncedInformer{},
		informerFactory:         factory,
		agentcubeInformer:       agentcubeFactory,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := ifm.RunAndWaitForCacheSync(ctx); err != nil {
		t.Fatalf("expected no error when all informers are synced, got %v", err)
	}
}
