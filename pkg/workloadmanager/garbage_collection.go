package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

type garbageCollector struct {
	k8sClient *K8sClient
	interval  time.Duration
}

func newGarbageCollector(k8sClient *K8sClient, interval time.Duration) *garbageCollector {
	return &garbageCollector{
		k8sClient: k8sClient,
		interval:  interval,
	}
}

func (gc *garbageCollector) run(stopCh <-chan struct{}) {
	ticker := time.NewTicker(gc.interval)
	for {
		select {
		case <-stopCh:
			ticker.Stop()
			log.Println("garbage collector stopped")
			return
		case <-ticker.C:
			gc.once()
		}
	}
}

func (gc *garbageCollector) once() {
	// Redis idle timeout
	// Redis reach DDL
	namespaces := []string{}
	names := []string{}
	errs := make([]error, 0, len(names))
	// delete sandbox
	for i := range names {
		err := gc.deleteSandbox(namespaces[i], names[i])
		if err != nil {
			errs = append(errs, err)
		}
	}
	err := utilerrors.NewAggregate(errs)
	if err != nil {
		log.Printf("garbage collector failed with error: %v", err)
		return
	}
	// Remove from Redis
}

func (gc *garbageCollector) deleteSandbox(namespace, name string) error {
	ctx, cancelFunc := context.WithTimeout(context.Background(), time.Minute)
	defer cancelFunc()
	err := gc.k8sClient.dynamicClient.Resource(SandboxGVR).Namespace(namespace).Delete(ctx, namespace, metav1.DeleteOptions{})
	if err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting sandbox %s/%s: %w", namespace, name, err)
	}
	return nil
}
