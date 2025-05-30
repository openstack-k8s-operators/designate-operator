/*
Copyright 2020 Red Hat

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
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	// Container image fall-back defaults

	// DesignateAPIContainerImage is the fall-back container image for DesignateAPI
	DesignateAPIContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-api:current-podified"
	// DesignateCentralContainerImage is the fall-back container image for DesignateCentral
	DesignateCentralContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-central:current-podified"
	// DesignateMdnsContainerImage is the fall-back container image for DesignateMdns
	DesignateMdnsContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-mdns:current-podified"
	// DesignateProducerContainerImage is the fall-back container image for DesignateProducer
	DesignateProducerContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-producer:current-podified"
	// DesignateWorkerContainerImage is the fall-back container image for DesignateWorker
	DesignateWorkerContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-worker:current-podified"
	// DesignateUnboundContainerImage is the fall-back container image for DesignateUnbound
	DesignateUnboundContainerImage = "quay.io/podified-antelope-centos9/openstack-unbound:current-podified"
	// DesignateBackendbind9ContainerImage is the fall-back container image for DesignateUnbound
	DesignateBackendbind9ContainerImage = "quay.io/podified-antelope-centos9/openstack-designate-backend-bind9:current-podified"
	// NetUtilsContainerImage is the container image containing support for predictable IP pod injection
	NetUtilsContainerImage = "quay.io/podified-antelope-centos9/openstack-netutils:current-podified"
)

// DesignateTemplate defines common input parameters used by all Designate services
type DesignateTemplate struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default=designate
	// ServiceUser - optional username used for this service to register in designate
	ServiceUser string `json:"serviceUser"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=designate
	// DatabaseAccount - name of MariaDBAccount which will be used to connect.
	DatabaseAccount string `json:"databaseAccount"`

	// +kubebuilder:validation:Optional
	// Secret containing OpenStack password information for DesignatePassword
	Secret string `json:"secret"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={service: DesignatePassword}
	// PasswordSelectors - Selectors to identify the DB and ServiceUser password from the Secret
	PasswordSelectors PasswordSelector `json:"passwordSelectors"`

	// +kubebuilder:validation:Optional
	// BackendType - Defines the backend service/configuration we are using, i.e. bind9, PowerDNS, BYO, etc..
	// Helps maintain a single init container/init.sh to do container setup
	BackendType string `json:"backendType"`

	// +kubebuilder:validation:Optional
	// BackendTypeProtocol - Defines the backend protocol to be used between the designate-worker &
	// designate_mdns to/from the DNS server. Acceptable values are: "UDP", "TCP"
	// Please Note: this MUST match what is in the /etc/designate.conf ['service:worker']
	BackendWorkerServerProtocol string `json:"backendWorkerServerProtocol"`

	// +kubebuilder:validation:Optional
	// BackendTypeProtocol - Defines the backend protocol to be used between the designate-worker &
	// designate_mdns to/from the DNS server. Acceptable values are: "UDP", "TCP"
	// Please Note: this MUST match what is in the /etc/designate.conf ['service:mdns']
	BackendMdnsServerProtocol string `json:"backendMdnsServerProtocol"`
}

// DesignateServiceTemplate defines the input parameters that can be defined for a given
// Designate service
type DesignateServiceTemplateCore struct {

	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running this service. Setting here overrides
	// any global NodeSelector settings within the Designate CR.
	NodeSelector *map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	// CustomServiceConfig - customize the service config using this parameter to change service defaults,
	// or overwrite rendered information using raw OpenStack config format. The content gets added to
	// to /etc/<service>/<service>.conf.d directory as a custom config file.
	CustomServiceConfig string `json:"customServiceConfig,omitempty"`

	// +kubebuilder:validation:Optional
	// CustomServiceConfigSecrets - customize the service config using this parameter to specify Secrets
	// that contain sensitive service config data. The content of each Secret gets added to the
	// /etc/<service>/<service>.conf.d directory as a custom config file.
	CustomServiceConfigSecrets []string `json:"customServiceConfigSecrets,omitempty"`

	// +kubebuilder:validation:Optional
	// ConfigOverwrite - interface to overwrite default config files like e.g. policy.json.
	// But can also be used to add additional files. Those get added to the service config dir in /etc/<service> .
	// TODO: -> implement
	DefaultConfigOverwrite map[string]string `json:"defaultConfigOverwrite,omitempty"`

	// +kubebuilder:validation:Optional
	// Resources - Compute Resources required by this service (Limits/Requests).
	// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Optional
	// NetworkAttachments is a list of NetworkAttachment resource names to expose the services to the given network
	NetworkAttachments []string `json:"networkAttachments,omitempty"`

	// +kubebuilder:validation:Optional
	// TopologyRef to apply the Topology defined by the associated CR referenced
	// by name
	TopologyRef *topologyv1.TopoRef `json:"topologyRef,omitempty"`
}

// DesignateServiceTemplate defines the input parameters that can be defined for a given
// Designate service
type DesignateServiceTemplate struct {
	// +kubebuilder:validation:Required
	// ContainerImage - Designate Container Image URL (will be set to environmental default if empty)
	ContainerImage string `json:"containerImage"`

	DesignateServiceTemplateCore `json:",inline"`
}

// PasswordSelector to identify the DB and AdminUser password from the Secret
type PasswordSelector struct {
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="DesignatePassword"
	// Service - Selector to get the designate service password from the Secret
	Service string `json:"service"`
}

// ValidateTopology -
func (instance *DesignateServiceTemplateCore) ValidateTopology(
	basePath *field.Path,
	namespace string,
) field.ErrorList {
	var allErrs field.ErrorList
	allErrs = append(allErrs, topologyv1.ValidateTopologyRef(
		instance.TopologyRef,
		*basePath.Child("topologyRef"), namespace)...)
	return allErrs
}
