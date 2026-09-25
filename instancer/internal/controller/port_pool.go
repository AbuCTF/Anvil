package controller

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	instv1 "github.com/anvil-lab/anvil/instancer/api/v1alpha1"
)

// Raw-TCP challenge instances (pwn/web3) are reached over PLAIN TCP on a
// per-instance port from a shared pool, so `nc host port` works and TCP
// half-close (which interactive solves rely on) is preserved end to end.
// Each pool port maps 1:1 to a challenge instance; the tcpproxy reads the lock
// ConfigMaps ({port, instance, backend}) to route, preserving TCP half-close
// (which Traefik's TCP proxy does not, so it can never deliver a flag).

const (
	portPoolLabel     = "instancer.anvil.dev/port-pool"
	portInstanceLabel = "instancer.anvil.dev/instance"
)

// PortPool is the contiguous pool of plain-TCP ports (inclusive) allocated to
// raw-TCP instances, plus the namespace holding the atomic lock objects.
type PortPool struct {
	Start     int
	End       int
	Namespace string
}

func (p PortPool) enabled() bool { return p.Start > 0 && p.End >= p.Start && p.Namespace != "" }

func portLockName(port int) string { return fmt.Sprintf("portlock-%d", port) }

// errPoolExhausted signals no free pool port; the reconciler surfaces it as a
// clean instance error + retries so a freed port is picked up.
type errPoolExhausted struct{ start, end int }

func (e errPoolExhausted) Error() string {
	return fmt.Sprintf("tcp port pool exhausted (%d-%d)", e.start, e.end)
}

// allocatePort returns the pool port already claimed by this instance for this
// backend, or atomically claims a free one. Idempotent: re-reconciles get the
// same port, and an instance exposing several raw-TCP ports gets a distinct port
// per backend. The lock ConfigMap (data {port, instance, backend}) is the source
// of truth: the tcpproxy reads it to route, and finalize releases it. A
// cluster-scoped owner reference is a GC backstop if the finalizer is skipped.
func (r *ChallengeInstanceReconciler) allocatePort(ctx context.Context, inst *instv1.ChallengeInstance, backend string) (int, error) {
	pool := r.Cfg.Pool
	instName := inst.Name

	// already claimed by this instance for this backend? (idempotent per port)
	held := &corev1.ConfigMapList{}
	if err := r.List(ctx, held, client.InNamespace(pool.Namespace),
		client.MatchingLabels{portPoolLabel: "true", portInstanceLabel: instName}); err != nil {
		return 0, err
	}
	for i := range held.Items {
		if held.Items[i].Data["backend"] != backend {
			continue
		}
		if p, err := strconv.Atoi(held.Items[i].Data["port"]); err == nil && p >= pool.Start && p <= pool.End {
			return p, nil
		}
	}

	// build the set of ports currently locked by any instance
	all := &corev1.ConfigMapList{}
	if err := r.List(ctx, all, client.InNamespace(pool.Namespace),
		client.MatchingLabels{portPoolLabel: "true"}); err != nil {
		return 0, err
	}
	used := make(map[int]bool, len(all.Items))
	for i := range all.Items {
		if p, err := strconv.Atoi(all.Items[i].Data["port"]); err == nil {
			used[p] = true
		}
	}

	// probe from a random offset: ports aren't guessable in sequence, and
	// concurrent launches rarely race for the same one. Create is atomic, so a
	// lost race just moves on.
	n := pool.End - pool.Start + 1
	off := rand.IntN(n)
	for i := range n {
		port := pool.Start + (off+i)%n
		if used[port] {
			continue
		}
		lock := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      portLockName(port),
				Namespace: pool.Namespace,
				Labels:    map[string]string{portPoolLabel: "true", portInstanceLabel: instName},
			},
			Data: map[string]string{"port": strconv.Itoa(port), "instance": instName, "backend": backend},
		}
		// GC backstop: a cluster-scoped ChallengeInstance may own a namespaced
		// dependent, so the lock is reclaimed if the CR is deleted without finalize.
		if err := controllerutil.SetOwnerReference(inst, lock, r.Scheme); err != nil {
			return 0, err
		}
		err := r.Create(ctx, lock)
		if err == nil {
			return port, nil
		}
		if !apierrors.IsAlreadyExists(err) {
			return 0, err
		}
		// lost the race for this port; try the next
	}
	return 0, errPoolExhausted{pool.Start, pool.End}
}

// releasePorts frees every pool port held by an instance (called on finalize).
func (r *ChallengeInstanceReconciler) releasePorts(ctx context.Context, instName string) error {
	locks := &corev1.ConfigMapList{}
	if err := r.List(ctx, locks, client.InNamespace(r.Cfg.Pool.Namespace),
		client.MatchingLabels{portPoolLabel: "true", portInstanceLabel: instName}); err != nil {
		return err
	}
	for i := range locks.Items {
		if err := r.Delete(ctx, &locks.Items[i]); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
