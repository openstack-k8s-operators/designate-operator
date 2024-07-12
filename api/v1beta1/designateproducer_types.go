/*
Copyright 2022.

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
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DesignateProducerSpecCore - this version has no containerImage for use with the OpenStackControlplane
type DesignateProducerSpecCore struct {
	// Common input parameters for the Designate Producer service
	DesignateServiceTemplateCore `json:",inline"`

	DesignateProducerSpecBase `json:",inline"`
}

// DesignateProducerSpec the desired state of DesignateProducer
type DesignateProducerSpec struct {
	// Common input parameters for the Designate Producer service
	DesignateServiceTemplate `json:",inline"`

	DesignateProducerSpecBase `json:",inline"`
}

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DesignateProducerSpec defines the desired state of DesignateProducer
type DesignateProducerSpecBase struct {
	// Common input parameters for all Designate services
	DesignateTemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Maximum=32
	// +kubebuilder:validation:Minimum=0
	// Replicas - Designate Producer Replicas
	Replicas *int32 `json:"replicas"`

	// +kubebuilder:validation:Optional
	// DatabaseHostname - Designate Database Hostname
	DatabaseHostname string `json:"databaseHostname,omitempty"`

	// +kubebuilder:validation:Optional
	// Secret containing RabbitMq transport URL
	TransportURLSecret string `json:"transportURLSecret,omitempty"`

	// +kubebuilder:validation:Optional
	// ServiceAccount - service account name used internally to provide Designate services the default SA name
	ServiceAccount string `json:"serviceAccount"`

	// +operator-sdk:csv:customresourcedefinitions:type=spec
	// TLS - Parameters related to the TLS
	TLS tls.Ca `json:"tls,omitempty"`
}

// DesignateProducerStatus defines the observed state of DesignateProducer
type DesignateProducerStatus struct {
	// ReadyCount of designate Producer instances
	ReadyCount int32 `json:"readyCount,omitempty"`

	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// NetworkAttachments status of the deployment pods
	NetworkAttachments map[string][]string `json:"networkAttachments,omitempty"`

	// ObservedGeneration - the most recent generation observed for this
	// service. If the observed generation is less than the spec generation,
	// then the controller has not processed the latest changes injected by
	// the opentack-operator in the top-level CR (e.g. the ContainerImage)
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="NetworkAttachments",type="string",JSONPath=".status.networkAttachments",description="NetworkAttachments"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// DesignateProducer is the Schema for the designateproducer API
type DesignateProducer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesignateProducerSpec   `json:"spec,omitempty"`
	Status DesignateProducerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesignateProducerList contains a list of DesignateProducer
type DesignateProducerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesignateProducer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesignateProducer{}, &DesignateProducerList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance DesignateProducer) IsReady() bool {
	return instance.Status.ReadyCount == *(instance.Spec.Replicas)
}
