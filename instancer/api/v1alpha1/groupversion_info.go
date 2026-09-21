// Package v1alpha1 defines the ChallengeInstance API for the Anvil instancer.
// +kubebuilder:object:generate=true
// +groupName=instancer.anvil.dev
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the group/version for this API.
	GroupVersion = schema.GroupVersion{Group: "instancer.anvil.dev", Version: "v1alpha1"}

	// SchemeBuilder registers the types with a runtime scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
