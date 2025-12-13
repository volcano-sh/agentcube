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

type garbageCollectorSandbox struct {
	Kind      string // Sandbox or SandboxClaim
	Name      string
	Namespace string
	SessionID string
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
	gcSandboxes := make([]garbageCollectorSandbox, 0, len(inactiveSandboxes)+len(expiredSandboxes))
	for _, inactive := range inactiveSandboxes {
		gcSandboxObj := garbageCollectorSandbox{
			Namespace: inactive.SandboxNamespace,
			SessionID: inactive.SessionID,
		}
		if inactive.SandboxClaimName != "" {
			gcSandboxObj.Kind = "SandboxClaim"
			gcSandboxObj.Name = inactive.SandboxClaimName
		} else {
			gcSandboxObj.Kind = "Sandbox"
			gcSandboxObj.Name = inactive.SandboxName
		}
		gcSandboxes = append(gcSandboxes, gcSandboxObj)
	}
	for _, expired := range expiredSandboxes {
		gcSandboxObj := garbageCollectorSandbox{
			Namespace: expired.SandboxNamespace,
			SessionID: expired.SessionID,
		}
		if expired.SandboxClaimName != "" {
			gcSandboxObj.Kind = "SandboxClaim"
			gcSandboxObj.Name = expired.SandboxClaimName
		} else {
			gcSandboxObj.Kind = "Sandbox"
			gcSandboxObj.Name = expired.SandboxName
		}
		gcSandboxes = append(gcSandboxes, gcSandboxObj)
	}

	if len(gcSandboxes) > 0 {
		log.Printf("garbage collector found %d sandboxes to be delete", len(gcSandboxes))
	}

	errs := make([]error, 0, len(gcSandboxes))
	// delete sandboxes
	for _, gcSandbox := range gcSandboxes {
		if gcSandbox.Kind == "SandboxClaim" {
			err = gc.deleteSandboxClaim(ctx, gcSandbox.Namespace, gcSandbox.Name)
		} else {
			err = gc.deleteSandbox(ctx, gcSandbox.Namespace, gcSandbox.Name)
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		log.Printf("garbage collector %s %s/%s session %s deleted", gcSandbox.Kind, gcSandbox.Namespace, gcSandbox.Name, gcSandbox.SessionID)
		err = gc.redisClient.DeleteSandboxBySessionIDTx(ctx, gcSandbox.SessionID)
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
