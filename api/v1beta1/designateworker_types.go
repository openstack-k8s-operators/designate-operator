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

// DesignateWorkerTemplate defines the input parameters for the Designate Scheduler service
type DesignateWorkerTemplate struct {
	// Common input parameters for the Designate Worker service
	DesignateServiceTemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// Replicas - Designate Worker Replicas
	Replicas int32 `json:"replicas"`
}

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DesignateWorkerSpec defines the desired state of DesignateWorker
type DesignateWorkerSpec struct {
	// Common input parameters for all Designate services
	DesignateTemplate `json:",inline"`

	// Input parameters for the Designate Scheduler service
	DesignateWorkerTemplate `json:",inline"`

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

// DesignateWorkerStatus defines the observed state of DesignateWorker
type DesignateWorkerStatus struct {
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

// DesignateWorker is the Schema for the designateworker
type DesignateWorker struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesignateWorkerSpec   `json:"spec,omitempty"`
	Status DesignateWorkerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesignateWorkerList contains a list of DesignateWorker
type DesignateWorkerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DesignateWorker `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DesignateWorker{}, &DesignateWorkerList{})
}

// IsReady - returns true if service is ready to serve requests
func (instance DesignateWorker) IsReady() bool {
	return instance.Status.ReadyCount == instance.Spec.Replicas
}
