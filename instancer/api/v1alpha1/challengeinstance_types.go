package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExposeType is how a port is published to players.
// +kubebuilder:validation:Enum=http;https;tcp-ssl
type ExposeType string

const (
	ExposeHTTP   ExposeType = "http"
	ExposeHTTPS  ExposeType = "https"
	ExposeTCPSSL ExposeType = "tcp-ssl" // raw TCP wrapped in TLS, routed by SNI on the shared port
)

// InstancePod is one pod of a challenge instance.
type InstancePod struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// ClusterIP service ports for this pod (peers resolve it by <name>).
	// +optional
	// +listType=atomic
	Ports []corev1.ServicePort `json:"ports,omitempty"`
	// Raw pod spec authored by the challenge; the operator applies secure defaults over a copy.
	Spec corev1.PodSpec `json:"spec"`
	// Opt-in outbound internet (still blocked from RFC1918 + the metadata server).
	// +optional
	Egress bool `json:"egress,omitempty"`
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
}

// InstanceExpose publishes one container port to players.
type InstanceExpose struct {
	Kind ExposeType `json:"kind"`
	// +kubebuilder:validation:MinLength=1
	HostPrefix string `json:"hostPrefix"`
	// +kubebuilder:validation:MinLength=1
	ContainerName string `json:"containerName"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	ContainerPort int32 `json:"containerPort"`
	// Challenge category — selects the subdomain, wildcard cert, and isolation profile.
	// +kubebuilder:validation:MinLength=1
	Category string `json:"category"`
	// +optional
	Title *string `json:"title,omitempty"`
}

// ChallengeInstanceSpec is the desired state of one per-team challenge instance.
type ChallengeInstanceSpec struct {
	// +kubebuilder:validation:MinLength=1
	TeamID string `json:"teamId"`
	// +kubebuilder:validation:MinLength=1
	ChallengeID string `json:"challengeId"`

	// Deadline; the API sets it (now+timeout) and extends by patching it.
	ExpiresAt metav1.Time `json:"expiresAt"`

	// Hard ceiling the reaper never lets ExpiresAt exceed, even across extends.
	// +optional
	MaxLifetime *metav1.Duration `json:"maxLifetime,omitempty"`

	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Pods []InstancePod `json:"pods"`

	// +optional
	// +listType=atomic
	Expose []InstanceExpose `json:"expose,omitempty"`

	// Injected into the exposed containers (per-team dynamic flags).
	// +optional
	Flags map[string]string `json:"flags,omitempty"`

	// Phase-2 pooler hook: pre-warmed, unclaimed until a team is assigned.
	// +optional
	Poolable bool `json:"poolable,omitempty"`
}

// InstanceEndpoint is a player-facing connection.
type InstanceEndpoint struct {
	Kind ExposeType `json:"kind"`
	Host string     `json:"host"`
	Port int32      `json:"port"`
	// +optional
	Title *string `json:"title,omitempty"`
	// URL for http(s); an nc/openssl string for tcp-ssl. Free-form, player-facing.
	Connect string `json:"connect"`
}

// ChallengeInstanceStatus is the observed state.
type ChallengeInstanceStatus struct {
	// Pending|Provisioning|Ready|Expiring|Errored — a cheap mirror of Conditions.
	// +optional
	Phase string `json:"phase,omitempty"`
	// +optional
	Namespace string `json:"namespace,omitempty"`
	// +optional
	// +listType=atomic
	Endpoints []InstanceEndpoint `json:"endpoints,omitempty"`
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=chalinst
// +kubebuilder:printcolumn:name="Team",type=string,JSONPath=`.spec.teamId`
// +kubebuilder:printcolumn:name="Challenge",type=string,JSONPath=`.spec.challengeId`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=`.spec.expiresAt`

// ChallengeInstance is one per-team challenge instance.
type ChallengeInstance struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ChallengeInstanceSpec   `json:"spec"`
	Status            ChallengeInstanceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ChallengeInstanceList is a list of ChallengeInstance.
type ChallengeInstanceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ChallengeInstance `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ChallengeInstance{}, &ChallengeInstanceList{})
}
