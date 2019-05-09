package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// GCPMachineProviderStatus is the type that will be embedded in a Machine.Status.ProviderStatus field.
// It contains GCP-specific status information.
// +k8s:openapi-gen=true
type GCPMachineProviderStatus struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// InstanceID is the ID of the instance in GCP
	// +optional
	InstanceID *string `json:"instanceId,omitempty"`

	// InstanceState is the provisioning state of the GCP Instance.
	// +optional
	InstanceState *string `json:"instanceState,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

func init() {
	SchemeBuilder.Register(&GCPMachineProviderStatus{})
}
