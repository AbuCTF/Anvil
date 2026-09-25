package controller

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/intstr"

	instv1 "github.com/anvil-lab/anvil/instancer/api/v1alpha1"
)

const (
	labelInstance  = "instancer.anvil.dev/instance"
	labelTeam      = "instancer.anvil.dev/team"
	labelChallenge = "instancer.anvil.dev/challenge"
	labelPod       = "instancer.anvil.dev/pod"
	labelManaged   = "instancer.anvil.dev/managed"
	finalizer      = "instancer.anvil.dev/finalizer"
	flagsSecret    = "instance-flags"
)

// baseLabels tie every object back to its instance for selection and GC.
func baseLabels(inst *instv1.ChallengeInstance) map[string]string {
	return map[string]string{
		labelManaged:   "true",
		labelInstance:  inst.Name,
		labelTeam:      inst.Spec.TeamID,
		labelChallenge: inst.Spec.ChallengeID,
	}
}

func buildNamespace(inst *instv1.ChallengeInstance) *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namespaceFor(inst.Name),
			Labels: baseLabels(inst),
		},
	}
}

// buildQuota caps total footprint per instance so one team cannot starve the node.
func buildQuota(ns string) *corev1.ResourceQuota {
	return &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{Name: "instance-quota", Namespace: ns},
		Spec: corev1.ResourceQuotaSpec{Hard: corev1.ResourceList{
			corev1.ResourceRequestsCPU:    resource.MustParse("2"),
			corev1.ResourceRequestsMemory: resource.MustParse("2Gi"),
			// multi-pod challenges (argus's reconciler adds its own pods) filled 4/4Gi
			corev1.ResourceLimitsCPU:    resource.MustParse("8"),
			corev1.ResourceLimitsMemory: resource.MustParse("8Gi"),
			corev1.ResourcePods:         resource.MustParse("12"),
		}},
	}
}

// buildLimitRange gives containers that declare no resources a small default,
// so an unbounded challenge image still counts against the quota.
func buildLimitRange(ns string) *corev1.LimitRange {
	return &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{Name: "instance-defaults", Namespace: ns},
		Spec: corev1.LimitRangeSpec{Limits: []corev1.LimitRangeItem{{
			Type:           corev1.LimitTypeContainer,
			Default:        corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("512Mi")},
			DefaultRequest: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("50m"), corev1.ResourceMemory: resource.MustParse("64Mi")},
		}}},
	}
}

// networkPolicies isolate the instance: deny everything, then re-open only DNS,
// intra-namespace traffic, and ingress from the ingress front ends — Traefik
// (web/HTTP) and the tcpproxy namespace (raw TCP).
func networkPolicies(inst *instv1.ChallengeInstance, ns, traefikNamespace, proxyNamespace string) []*netv1.NetworkPolicy {
	all := []netv1.PolicyType{netv1.PolicyTypeIngress, netv1.PolicyTypeEgress}
	proto := func(p corev1.Protocol) *corev1.Protocol { return &p }
	dns := intstr.FromInt32(53)

	denyAll := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "default-deny", Namespace: ns},
		Spec:       netv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{}, PolicyTypes: all},
	}

	allowDNS := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-dns", Namespace: ns},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{{
				To:    []netv1.NetworkPolicyPeer{{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": "kube-system"}}}},
				Ports: []netv1.NetworkPolicyPort{{Protocol: proto(corev1.ProtocolUDP), Port: &dns}, {Protocol: proto(corev1.ProtocolTCP), Port: &dns}},
			}},
		},
	}

	// Pods of one instance may talk to each other (multi-pod challenges) but nothing else internal.
	intra := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-intra", Namespace: ns},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{},
			PolicyTypes: all,
			Ingress:     []netv1.NetworkPolicyIngressRule{{From: []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}}},
			Egress:      []netv1.NetworkPolicyEgressRule{{To: []netv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{}}}}},
		},
	}

	// The ingress front ends reach only the pods that expose a port to players:
	// Traefik (web/HTTP :443) and the tcpproxy namespace (raw TCP pool ports).
	from := []netv1.NetworkPolicyPeer{
		{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": traefikNamespace}}},
	}
	if proxyNamespace != "" && proxyNamespace != traefikNamespace {
		from = append(from, netv1.NetworkPolicyPeer{NamespaceSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/metadata.name": proxyNamespace}}})
	}
	fromIngress := &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-ingress", Namespace: ns},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"instancer.anvil.dev/exposed": "true"}},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeIngress},
			Ingress:     []netv1.NetworkPolicyIngressRule{{From: from}},
		},
	}

	return []*netv1.NetworkPolicy{denyAll, allowDNS, intra, fromIngress}
}

// egressPolicy opts one pod into outbound internet while still blocking the
// cluster's private ranges and the cloud metadata server.
func egressPolicy(ns, podName string) *netv1.NetworkPolicy {
	return &netv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-egress-" + podName, Namespace: ns},
		Spec: netv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{labelPod: podName}},
			PolicyTypes: []netv1.PolicyType{netv1.PolicyTypeEgress},
			Egress: []netv1.NetworkPolicyEgressRule{{
				To: []netv1.NetworkPolicyPeer{{IPBlock: &netv1.IPBlock{
					CIDR: "0.0.0.0/0",
					Except: []string{
						"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
						"169.254.169.254/32", // metadata server
						"100.64.0.0/10",      // GKE VPC-native pod/service ranges
					},
				}}},
			}},
		},
	}
}

// serviceName is the ClusterIP service for a pod. It is identity (not "svc-"+pod)
// so multi-container peers resolve each other by role name (e.g. http://scanner:8081).
// Internal-only, symmetric across the writer (buildService) and readers (routes +
// tcpproxy backend), so single-container challenges are unaffected.
func serviceName(pod string) string { return pod }

func buildService(inst *instv1.ChallengeInstance, pod instv1.InstancePod) *corev1.Service {
	ports := pod.Ports
	if len(ports) == 0 {
		return nil
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName(pod.Name),
			Namespace: namespaceFor(inst.Name),
			Labels:    baseLabels(inst),
		},
		Spec: corev1.ServiceSpec{
			Selector: map[string]string{labelPod: pod.Name},
			Ports:    ports,
			Type:     corev1.ServiceTypeClusterIP,
		},
	}
}

// buildPod applies hardened defaults over the challenge-authored spec: no
// service-account token, gVisor sandbox, seccomp, and dropped capabilities.
// The authored spec still wins where it sets a field explicitly.
func buildPod(inst *instv1.ChallengeInstance, pod instv1.InstancePod, cfg Config, exposedPods map[string]bool) *corev1.Pod {
	spec := *pod.Spec.DeepCopy()
	runtimeClass := cfg.RuntimeClass

	no := false
	spec.AutomountServiceAccountToken = &no
	spec.EnableServiceLinks = &no
	spec.HostNetwork = false
	spec.HostPID = false
	spec.HostIPC = false
	if spec.RestartPolicy == "" {
		spec.RestartPolicy = corev1.RestartPolicyAlways
	}
	// challenge servers hold no state worth a graceful stop; a 30s default made
	// teardown (and a team's relaunch waiting on it) crawl in a wave of expiries.
	if spec.TerminationGracePeriodSeconds == nil {
		grace := int64(5)
		spec.TerminationGracePeriodSeconds = &grace
	}
	if runtimeClass != "" && spec.RuntimeClassName == nil {
		rc := runtimeClass
		spec.RuntimeClassName = &rc
	}
	if spec.SecurityContext == nil {
		spec.SecurityContext = &corev1.PodSecurityContext{}
	}
	// leave a dead node after 60s instead of the default 5 min, so a preempted
	// spot node's instances come back on a live one quickly.
	gone := int64(60)
	for _, key := range []string{"node.kubernetes.io/not-ready", "node.kubernetes.io/unreachable"} {
		spec.Tolerations = append(spec.Tolerations, corev1.Toleration{
			Key: key, Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute, TolerationSeconds: &gone,
		})
	}
	if spec.SecurityContext.SeccompProfile == nil {
		spec.SecurityContext.SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
	}

	flagEnv := flagsEnv(inst.Spec.Flags)
	for i := range spec.Containers {
		hardenContainer(&spec.Containers[i], flagEnv)
		reserveRequests(&spec.Containers[i], cfg.CPURequestPct, cfg.MemRequestPct)
	}
	for i := range spec.InitContainers {
		hardenContainer(&spec.InitContainers[i], flagEnv)
		reserveRequests(&spec.InitContainers[i], cfg.CPURequestPct, cfg.MemRequestPct)
	}

	labels := baseLabels(inst)
	labels[labelPod] = pod.Name
	for k, v := range pod.Labels {
		labels[k] = v
	}
	if exposedPods[pod.Name] {
		labels["instancer.anvil.dev/exposed"] = "true"
	}

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: namespaceFor(inst.Name),
			Labels:    labels,
			// a live instance is never evicted to consolidate nodes; nodes drain as instances expire.
			Annotations: map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "false"},
		},
		Spec: spec,
	}
}

// reserveRequests sets requests to a slice of the limits when only limits are
// given. k8s otherwise copies limits into requests, so an idle challenge would
// reserve its whole limit and a node would hold a handful of instances.
func reserveRequests(c *corev1.Container, cpuPct, memPct int64) {
	if c.Resources.Requests != nil || c.Resources.Limits == nil {
		return
	}
	// floors cover the sandbox's own overhead, but never above the limit itself
	// (a request over its limit makes the pod invalid).
	req := corev1.ResourceList{}
	if cpu, ok := c.Resources.Limits[corev1.ResourceCPU]; ok && cpuPct > 0 {
		m := cpu.MilliValue()
		req[corev1.ResourceCPU] = *resource.NewMilliQuantity(max(m*cpuPct/100, min(10, m)), resource.DecimalSI)
	}
	if mem, ok := c.Resources.Limits[corev1.ResourceMemory]; ok && memPct > 0 {
		b := mem.Value()
		req[corev1.ResourceMemory] = *resource.NewQuantity(max(b*memPct/100, min(64<<20, b)), resource.BinarySI)
	}
	if len(req) > 0 {
		c.Resources.Requests = req
	}
}

func hardenContainer(c *corev1.Container, flagEnv []corev1.EnvVar) {
	if c.SecurityContext == nil {
		c.SecurityContext = &corev1.SecurityContext{}
	}
	if c.SecurityContext.AllowPrivilegeEscalation == nil {
		no := false
		c.SecurityContext.AllowPrivilegeEscalation = &no
	}
	if c.SecurityContext.Capabilities == nil {
		c.SecurityContext.Capabilities = &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}
	}
	c.Env = append(c.Env, flagEnv...)
}

func flagsEnv(flags map[string]string) []corev1.EnvVar {
	if len(flags) == 0 {
		return nil
	}
	out := make([]corev1.EnvVar, 0, len(flags))
	for k, v := range flags {
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

// ingressRoute is a Traefik HTTP(S) route, built unstructured to avoid pulling
// in the whole Traefik module as a dependency. Empty tls uses Traefik's default
// TLSStore (a multi-SAN wildcard cert), so instances need no per-namespace secret.
func ingressRoute(inst *instv1.ChallengeInstance, ns, name, hostname, svc, entryPoint string, port int32) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRoute",
		"metadata":   map[string]any{"name": name, "namespace": ns, "labels": toAny(baseLabels(inst))},
		"spec": map[string]any{
			"entryPoints": []any{entryPoint},
			"routes": []any{map[string]any{
				"match": fmt.Sprintf("Host(`%s`)", hostname),
				"kind":  "Rule",
				"services": []any{map[string]any{
					"name": svc,
					"port": int64(port),
				}},
			}},
			"tls": map[string]any{}, // default TLSStore
		},
	})
	return u
}

// ingressRouteTCP terminates TLS with the default wildcard cert and forwards
// raw TCP to the pod, routed by SNI on the category's shared TCP entrypoint.
func ingressRouteTCP(inst *instv1.ChallengeInstance, ns, name, hostname, svc string, port int32, entryPoint string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetUnstructuredContent(map[string]any{
		"apiVersion": "traefik.io/v1alpha1",
		"kind":       "IngressRouteTCP",
		"metadata":   map[string]any{"name": name, "namespace": ns, "labels": toAny(baseLabels(inst))},
		"spec": map[string]any{
			"entryPoints": []any{entryPoint},
			"routes": []any{map[string]any{
				"match": fmt.Sprintf("HostSNI(`%s`)", hostname),
				"services": []any{map[string]any{
					"name": svc,
					"port": int64(port),
				}},
			}},
			"tls": map[string]any{}, // default TLSStore, matched by SNI
		},
	})
	return u
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
