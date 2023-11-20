/*
Copyright 2023.

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

package v1beta1

import (
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DesignateUnboundSpec defines the desired state of DesignateUnbound
type DesignateUnboundSpec struct {
	// Common input parameters for the Designate Unbound service
	DesignateServiceTemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// ServiceAccount - service account name used internally to provide Designate services the default SA name
	ServiceAccount string `json:"serviceAccount"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Maximum=32
	// +kubebuilder:validation:Minimum=0
	// Replicas - Designate Unbound Replicas
	Replicas *int32 `json:"replicas"`
}

// DesignateUnboundStatus defines the observed state of DesignateUnbound
type DesignateUnboundStatus struct {
	// ReadyCount of designate central instances
	ReadyCount int32 `json:"readyCount,omitempty"`

	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// NetworkAttachments status of the deployment pods
	NetworkAttachments map[string][]string `json:"networkAttachments,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// DesignateUnbound is the Schema for the designateworker API
type DesignateUnbound struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesignateUnboundSpec   `json:"spec,omitempty"`
	Status DesignateUnboundStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesignateUnboundList contains a list of DesignateUnbound
type DesignateUnboundList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesignateUnbound `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesignateUnbound{}, &DesignateUnboundList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance DesignateUnbound) IsReady() bool {
	return instance.Status.ReadyCount == *(instance.Spec.Replicas)
}
