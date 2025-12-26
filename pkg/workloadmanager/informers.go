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

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
)

var (
	AgentRuntimeGVR = schema.GroupVersionResource{
		Group:    "runtime.agentcube.volcano.sh",
		Version:  "v1alpha1",
		Resource: "agentruntimes",
	}
	CodeInterpreterGVR = schema.GroupVersionResource{
		Group:    "runtime.agentcube.volcano.sh",
		Version:  "v1alpha1",
		Resource: "codeinterpreters",
	}
	SandboxGVR = schema.GroupVersionResource{
		Group:    "agents.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "sandboxes",
	}
	SandboxClaimGVR = schema.GroupVersionResource{
		Group:    "extensions.agents.x-k8s.io",
		Version:  "v1alpha1",
		Resource: "sandboxclaims",
	}
)

type Informers struct {
	AgentRuntimeInformer    cache.SharedIndexInformer
	CodeInterpreterInformer cache.SharedIndexInformer
	PodInformer             cache.SharedIndexInformer
	informerFactory         informers.SharedInformerFactory
}

func NewInformers(k8sClient *K8sClient) *Informers {
	return &Informers{
		AgentRuntimeInformer:    k8sClient.dynamicInformer.ForResource(AgentRuntimeGVR).Informer(),
		CodeInterpreterInformer: k8sClient.dynamicInformer.ForResource(CodeInterpreterGVR).Informer(),
		PodInformer:             k8sClient.podInformer,
		informerFactory:         k8sClient.informerFactory,
	}
}

func (ifm *Informers) RunAndWaitForCacheSync(ctx context.Context) error {
	ifm.run(ctx.Done())
	ctxTimeout, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	if err := ifm.waitForCacheSync(ctxTimeout); err != nil {
		return fmt.Errorf("failed to wait for caches to sync: %w", err)
	}
	return nil
}

func (ifm *Informers) run(stopCh <-chan struct{}) {
	ifm.informerFactory.Start(stopCh)
	go ifm.AgentRuntimeInformer.Run(stopCh)
	go ifm.CodeInterpreterInformer.Run(stopCh)
}

func (ifm *Informers) waitForCacheSync(ctx context.Context) error {
	if !cache.WaitForCacheSync(ctx.Done(), ifm.AgentRuntimeInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for %v caches to sync", AgentRuntimeGVR)
	}
	if !cache.WaitForCacheSync(ctx.Done(), ifm.CodeInterpreterInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for %v caches to sync", CodeInterpreterGVR)
	}
	if !cache.WaitForCacheSync(ctx.Done(), ifm.PodInformer.HasSynced) {
		return fmt.Errorf("timed out waiting for pod informer cache to sync")
	}
	return nil
}
