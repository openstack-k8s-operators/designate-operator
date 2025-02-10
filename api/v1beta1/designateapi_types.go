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
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DesignateAPITemplate defines the input parameters for the Designate API service
type DesignateAPITemplate struct {
	// Common input parameters for the Designate API service
	DesignateServiceTemplate `json:",inline"`
}

// DesignateAPISpecCore this version has no containerImage for use with the OpenStackControlplane
type DesignateAPISpecCore struct {

	// Common input parameters for all Designate services
	DesignateAPISpecBase `json:",inline"`

	DesignateServiceTemplateCore `json:",inline"`
}

// DesignateAPISpec defines the desired state of DesignateAPI
type DesignateAPISpec struct {

	// Common input parameters for all Designate services
	DesignateAPISpecBase `json:",inline"`

	DesignateServiceTemplate `json:",inline"`
}

// DesignateAPISpecBase -
type DesignateAPISpecBase struct {
	// Common input parameters for all Designate services
	DesignateTemplate `json:",inline"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Maximum=32
	// +kubebuilder:validation:Minimum=0
	// Replicas - Designate API Replicas
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

	// Override, provides the ability to override the generated manifest of several child resources.
	Override APIOverrideSpec `json:"override,omitempty"`

	// +kubebuilder:validation:Optional
	// +operator-sdk:csv:customresourcedefinitions:type=spec
	// TLS - Parameters related to the TLS
	TLS tls.API `json:"tls,omitempty"`

	// +kubebuilder:validation:Optional
	// APITimeout for HAProxy and Apache defaults to DesignateSpecCore APITimeout (seconds)
	APITimeout int `json:"apiTimeout"`

	// +kubebuilder:validation:Optional
	// HttpdCustomization - customize the httpd service
	HttpdCustomization HttpdCustomization `json:"httpdCustomization,omitempty"`
}

// APIOverrideSpec to override the generated manifest of several child resources.
type APIOverrideSpec struct {
	// Override configuration for the Service created to serve traffic to the cluster.
	// The key must be the endpoint type (public, internal)
	Service map[service.Endpoint]service.RoutedOverrideSpec `json:"service,omitempty"`
}

// HttpdCustomization - customize the httpd service
type HttpdCustomization struct {
	// +kubebuilder:validation:Optional
	// CustomConfigSecret - customize the httpd vhost config using this parameter to specify
	// a secret that contains service config data. The content of each provided snippet gets
	// rendered as a go template and placed into /etc/httpd/conf/httpd_custom_<key> .
	// In the default httpd template at the end of the vhost those custom configs get
	// included using `Include conf/httpd_custom_<endpoint>_*`.
	// For information on how sections in httpd configuration get merged, check section
	// "How the sections are merged" in https://httpd.apache.org/docs/current/sections.html#merging
	CustomConfigSecret *string `json:"customConfigSecret,omitempty"`
}

// DesignateAPIStatus defines the observed state of DesignateAPI
type DesignateAPIStatus struct {
	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

	// API endpoints
	APIEndpoints map[string]map[string]string `json:"apiEndpoints,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// ReadyCount of designate API instances
	ReadyCount int32 `json:"readyCount,omitempty"`

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
	return instance.Status.ReadyCount == *(instance.Spec.Replicas)
}
