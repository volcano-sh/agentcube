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

	cubeinformers "github.com/volcano-sh/agentcube/client-go/informers/externalversions"
	cubelisters "github.com/volcano-sh/agentcube/client-go/listers/runtime/v1alpha1"
	runtimev1alpha1 "github.com/volcano-sh/agentcube/pkg/apis/runtime/v1alpha1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"
	sandboxv1beta1 "sigs.k8s.io/agent-sandbox/api/v1beta1"
	extensionsv1beta1 "sigs.k8s.io/agent-sandbox/extensions/api/v1beta1"
)

var (
	AgentRuntimeGVR    = runtimev1alpha1.GroupVersion.WithResource("agentruntimes")
	CodeInterpreterGVR = runtimev1alpha1.GroupVersion.WithResource("codeinterpreters")
	SandboxGVR         = sandboxv1beta1.GroupVersion.WithResource("sandboxes")
	SandboxClaimGVR    = extensionsv1beta1.GroupVersion.WithResource("sandboxclaims")
)

type Informers struct {
	AgentRuntimeLister      cubelisters.AgentRuntimeLister
	CodeInterpreterLister   cubelisters.CodeInterpreterLister
	AgentRuntimeInformer    cache.SharedIndexInformer
	CodeInterpreterInformer cache.SharedIndexInformer
	PodInformer             cache.SharedIndexInformer
	informerFactory         informers.SharedInformerFactory
	cubeInformerFactory     cubeinformers.SharedInformerFactory
}

func NewInformers(k8sClient *K8sClient) *Informers {
	agentRuntimeInformer := k8sClient.cubeInformerFactory.Runtime().V1alpha1().AgentRuntimes()
	codeInterpreterInformer := k8sClient.cubeInformerFactory.Runtime().V1alpha1().CodeInterpreters()

	return &Informers{
		AgentRuntimeLister:      agentRuntimeInformer.Lister(),
		CodeInterpreterLister:   codeInterpreterInformer.Lister(),
		AgentRuntimeInformer:    agentRuntimeInformer.Informer(),
		CodeInterpreterInformer: codeInterpreterInformer.Informer(),
		PodInformer:             k8sClient.podInformer,
		informerFactory:         k8sClient.informerFactory,
		cubeInformerFactory:     k8sClient.cubeInformerFactory,
	}
}

func (ifm *Informers) RunAndWaitForCacheSync(ctx context.Context) error {
	ifm.run(ctx.Done())
	ctxTimeout, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	if err := ifm.waitForCacheSync(ctxTimeout); err != nil {
		return fmt.Errorf("failed to wait for caches to sync: %w", err)
	}
	return nil
}

func (ifm *Informers) run(stopCh <-chan struct{}) {
	ifm.informerFactory.Start(stopCh)
	ifm.cubeInformerFactory.Start(stopCh)
}

func (ifm *Informers) waitForCacheSync(ctx context.Context) error {
	if !cache.WaitForCacheSync(ctx.Done(), ifm.AgentRuntimeInformer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for %v caches to sync: %w", AgentRuntimeGVR, err)
		}
		return fmt.Errorf("timed out waiting for %v caches to sync", AgentRuntimeGVR)
	}
	if !cache.WaitForCacheSync(ctx.Done(), ifm.CodeInterpreterInformer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for %v caches to sync: %w", CodeInterpreterGVR, err)
		}
		return fmt.Errorf("timed out waiting for %v caches to sync", CodeInterpreterGVR)
	}
	if !cache.WaitForCacheSync(ctx.Done(), ifm.PodInformer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for pod informer cache to sync: %w", err)
		}
		return fmt.Errorf("timed out waiting for pod informer cache to sync")
	}
	return nil
}
