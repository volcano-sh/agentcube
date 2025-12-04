package workloadmanager

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/volcano-sh/agentcube/pkg/redis"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
)

const (
	gcOnceTimeout = 2 * time.Minute
)

type garbageCollector struct {
	k8sClient   *K8sClient
	interval    time.Duration
	redisClient redis.Client
}

func newGarbageCollector(k8sClient *K8sClient, redisClient redis.Client, interval time.Duration) *garbageCollector {
	return &garbageCollector{
		k8sClient:   k8sClient,
		interval:    interval,
		redisClient: redisClient,
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
	// List sandboxes idle timeout
	ctx, cancel := context.WithTimeout(context.Background(), gcOnceTimeout)
	defer cancel()
	inactiveTime := time.Now().Add(-DefaultSandboxIdleTimeout)
	inactiveSandboxes, err := gc.redisClient.ListInactiveSandboxes(ctx, inactiveTime, 16)
	if err != nil {
		log.Printf("garbage collector error listing inactive sandboxes: %v", err)
	}
	// List sandboxes reach DDL
	expiredSandboxes, err := gc.redisClient.ListExpiredSandboxes(ctx, time.Now(), 16)
	if err != nil {
		log.Printf("garbage collector error listing expired sandboxes: %v", err)
	}
	namespaces := make([]string, 0, len(inactiveSandboxes)+len(expiredSandboxes))
	names := make([]string, 0, len(inactiveSandboxes)+len(expiredSandboxes))
	sessionIDs := make([]string, 0, len(inactiveSandboxes)+len(expiredSandboxes))
	for _, inactive := range inactiveSandboxes {
		namespaces = append(namespaces, inactive.SandboxNamespace)
		names = append(names, inactive.SandboxName)
		sessionIDs = append(sessionIDs, inactive.SessionID)
	}
	for _, expired := range expiredSandboxes {
		namespaces = append(namespaces, expired.SandboxNamespace)
		names = append(names, expired.SandboxName)
		sessionIDs = append(sessionIDs, expired.SessionID)
	}

	if len(names) > 0 {
		log.Printf("garbage collector found %d sandboxes to be delete", len(names))
	}

	errs := make([]error, 0, len(names))
	// delete sandboxes
	for i := range names {
		err = gc.deleteSandbox(ctx, namespaces[i], names[i])
		if err != nil {
			errs = append(errs, err)
			continue
		}
		log.Printf("garbage collector sandbox %s/%s session %s deleted", namespaces[i], names[i], sessionIDs[i])
		err = gc.redisClient.DeleteSandboxBySessionIDTx(ctx, sessionIDs[i])
		if err != nil {
			errs = append(errs, err)
		}
	}
	err = utilerrors.NewAggregate(errs)
	if err != nil {
		log.Printf("garbage collector failed with error: %v", err)
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
