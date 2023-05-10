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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DesignateAPITemplate defines the input parameters for the Designate API service
type DesignateAPITemplate struct {
	// Common input parameters for the Designate API service
	DesignateServiceTemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// Replicas - Designate API Replicas
	Replicas int32 `json:"replicas"`
}

// DesignateAPISpec defines the desired state of DesignateAPI
type DesignateAPISpec struct {
	// Common input parameters for all Designate services
	DesignateTemplate `json:",inline"`

	// Input parameters for the Designate Scheduler service
	DesignateAPITemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// DatabaseHostname - Designate Database Hostname
	DatabaseHostname string `json:"databaseHostname,omitempty"`

	// +kubebuilder:validation:Optional
	// Secret containing RabbitMq transport URL
	TransportURLSecret string `json:"transportURLSecret,omitempty"`

	// +kubebuilder:validation:Optional
	// ServiceAccount - service account name used internally to provide Designate services the default SA name
	ServiceAccount string `json:"serviceAccount"`
}

// DesignateAPIStatus defines the observed state of DesignateAPI
type DesignateAPIStatus struct {
	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// API endpoint
	APIEndpoints map[string]map[string]string `json:"apiEndpoint,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// ReadyCount of designate API instances
	ReadyCount int32 `json:"readyCount,omitempty"`

	// ServiceIDs - the ID of the registered service in keystone
	ServiceIDs map[string]string `json:"serviceIDs,omitempty"`

	// NetworkAttachments status of the deployment pods
	NetworkAttachments map[string][]string `json:"networkAttachments,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="NetworkAttachments",type="string",JSONPath=".status.networkAttachments",description="NetworkAttachments"
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// DesignateAPI is the Schema for the designateapis API
type DesignateAPI struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesignateAPISpec   `json:"spec,omitempty"`
	Status DesignateAPIStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesignateAPIList contains a list of DesignateAPI
type DesignateAPIList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesignateAPI `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesignateAPI{}, &DesignateAPIList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance DesignateAPI) IsReady() bool {
	return instance.Status.ReadyCount == instance.Spec.Replicas
}
