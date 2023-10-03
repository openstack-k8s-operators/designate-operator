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
	"github.com/openstack-k8s-operators/lib-common/modules/storage"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DbSyncHash hash
	DbSyncHash = "dbsync"

	// DeploymentHash hash used to detect changes
	DeploymentHash = "deployment"
)

// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DesignateSpec defines the desired state of Designate
type DesignateSpec struct {
	// INSERT ADDITIONAL SPEC FIELDS - desired state of cluster
	// Important: Run "make" to regenerate code after modifying this file

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=designate
	// ServiceUser - optional username used for this service to register in designate
	ServiceUser string `json:"serviceUser"`

	// +kubebuilder:validation:Optional
	// DatabaseHostname - Designate Database Hostname
	// DatabaseHostname string `json:"databaseHostname,omitempty"`

	// +kubebuilder:validation:Required
	// MariaDB instance name
	// Right now required by the maridb-operator to get the credentials from the instance to create the DB
	// Might not be required in future
	DatabaseInstance string `json:"databaseInstance"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=designate
	// DatabaseUser - optional username used for designate DB, defaults to designate
	// TODO: -> implement needs work in mariadb-operator, right now only designate
	DatabaseUser string `json:"databaseUser"`

	// +kubebuilder:validation:Required
	// +kubebuilder:default=rabbitmq
	// RabbitMQ instance name
	// Needed to request a transportURL that is created and used in Designate
	RabbitMqClusterName string `json:"rabbitMqClusterName"`

	// +kubebuilder:validation:Required
	// Secret containing OpenStack password information for designate DesignateDatabasePassword, AdminPassword
	Secret string `json:"secret"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default={database: DesignateDatabasePassword, service: DesignatePassword}
	// PasswordSelectors - Selectors to identify the DB and AdminUser password from the Secret
	PasswordSelectors PasswordSelector `json:"passwordSelectors,omitempty"`

	// +kubebuilder:validation:Optional
	// BackendType - Defines the backend service/configuration we are using, i.e. bind9, unhbound, PowerDNS, BYO, etc..
	// Helps maintain a single init container/init.sh to do container setup
	BackendType string `json:"None"`

	// +kubebuilder:validation:Optional
	// BackendTypeProtocol - Defines the backend protocol to be used between the desigante-worker &
	// desigante_mdns to/from the DNS server. Acceptable values are: "UDP", "TCP"
	// Please Note: this MUST match what is in the /etc/designate.conf ['service:worker']
	BackendWorkerServerProtocol string `json:"backendWorkerServerProtocol"`

	// +kubebuilder:validation:Optional
	// BackendTypeProtocol - Defines the backend protocol to be used between the desigante-worker &
	// desigante_mdns to/from the DNS server. Acceptable values are: "UDP", "TCP"
	// Please Note: this MUST match what is in the /etc/designate.conf ['service:mdns']
	BackendMdnsServerProtocol string `json:"backendMdnsServerProtocol"`

	// +kubebuilder:validation:Optional
	// Debug - enable debug for different deploy stages. If an init container is used, it runs and the
	// actual action pod gets started with sleep infinity
	Debug DesignateDebug `json:"debug,omitempty"`

	// +kubebuilder:validation:Optional
	// NodeSelector to target subset of worker nodes running this service
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default=false
	// PreserveJobs - do not delete jobs after they finished e.g. to check logs
	PreserveJobs bool `json:"preserveJobs,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:default="# add your customization here"
	// CustomServiceConfig - customize the service config using this parameter to change service defaults,
	// or overwrite rendered information using raw OpenStack config format. The content gets added to
	// to /etc/<service>/<service>.conf.d directory as custom.conf file.
	CustomServiceConfig string `json:"customServiceConfig,omitempty"`

	// +kubebuilder:validation:Optional
	// ConfigOverwrite - interface to overwrite default config files like e.g. logging.conf or policy.json.
	// But can also be used to add additional files. Those get added to the service config dir in /etc/<service> .
	// TODO: -> implement
	DefaultConfigOverwrite map[string]string `json:"defaultConfigOverwrite,omitempty"`

	// +kubebuilder:validation:Optional
	// Resources - Compute Resources required by this service (Limits/Requests).
	// https://kubernetes.io/docs/concepts/configuration/manage-resources-containers/
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:Required
	// DesignateAPI - Spec definition for the API service of this Designate deployment
	DesignateAPI DesignateAPISpec `json:"designateAPI"`

	// +kubebuilder:validation:Required
	// DesignateCentral - Spec definition for the Central service of this Designate deployment
	DesignateCentral DesignateCentralSpec `json:"designateCentral"`

	// +kubebuilder:validation:Required
	// DesignateWorker - Spec definition for the Worker service of this Designate deployment
	DesignateWorker DesignateWorkerSpec `json:"designateWorker"`

	// +kubebuilder:validation:Required
	// DesignateMdns - Spec definition for the Mdns service of this Designate deployment
	DesignateMdns DesignateMdnsSpec `json:"designateMdns"`

	// +kubebuilder:validation:Required
	// DesignateProducer - Spec definition for the Producer service of this Designate deployment
	DesignateProducer DesignateProducerSpec `json:"designateProducer"`
}

// DesignateStatus defines the observed state of Designate
type DesignateStatus struct {
	// Map of hashes to track e.g. job status
	Hash map[string]string `json:"hash,omitempty"`

  // API endpoint
  APIEndpoints map[string]string `json:"apiEndpoint,omitempty"`

	// Conditions
	Conditions condition.Conditions `json:"conditions,omitempty" optional:"true"`

	// Designate Database Hostname
	DatabaseHostname string `json:"databaseHostname,omitempty"`

	// TransportURLSecret - Secret containing RabbitMQ transportURL
	TransportURLSecret string `json:"transportURLSecret,omitempty"`

	// ReadyCount of Designate API instance
	DesignateAPIReadyCount int32 `json:"designateAPIReadyCount,omitempty"`

	// ReadyCount of Designate Central instance
	DesignateCentralReadyCount int32 `json:"designateCentralReadyCount,omitempty"`

	// ReadyCount of Designate Worker instance
	DesignateWorkerReadyCount int32 `json:"designateWorkerReadyCount,omitempty"`

	// ReadyCount of Designate Mdns instance
	DesignateMdnsReadyCount int32 `json:"designateMdnsReadyCount,omitempty"`

	// ReadyCount of Designate Producer instance
	DesignateProducerReadyCount int32 `json:"designateProducerReadyCount,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type="string",JSONPath=".status.conditions[0].status",description="Status"
// +kubebuilder:printcolumn:name="Message",type="string",JSONPath=".status.conditions[0].message",description="Message"

// Designate is the Schema for the designates API
type Designate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DesignateSpec   `json:"spec,omitempty"`
	Status DesignateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DesignateList contains a list of Designate
type DesignateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Designate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Designate{}, &DesignateList{})
}

// IsReady - returns true if all subresources Ready condition is true
func (instance Designate) IsReady() bool {
	return instance.Status.Conditions.IsTrue(DesignateAPIReadyCondition) &&
		instance.Status.Conditions.IsTrue(DesignateCentralReadyCondition) &&
		instance.Status.Conditions.IsTrue(DesignateWorkerReadyCondition) &&
		instance.Status.Conditions.IsTrue(DesignateMdnsReadyCondition) &&
		instance.Status.Conditions.IsTrue(DesignateProducerReadyCondition)
}

// DesignateExtraVolMounts exposes additional parameters processed by the designate-operator
// and defines the common VolMounts structure provided by the main storage module
type DesignateExtraVolMounts struct {
	// +kubebuilder:validation:Optional
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:Optional
	Region string `json:"region,omitempty"`
	// +kubebuilder:validation:Required
	VolMounts []storage.VolMounts `json:"extraVol"`
}

// Propagate is a function used to filter VolMounts according to the specified
// PropagationType array
func (c *DesignateExtraVolMounts) Propagate(svc []storage.PropagationType) []storage.VolMounts {

	var vl []storage.VolMounts

	for _, gv := range c.VolMounts {
		vl = append(vl, gv.Propagate(svc)...)
	}

	return vl
}

// RbacConditionsSet - set the conditions for the rbac object
func (instance Designate) RbacConditionsSet(c *condition.Condition) {
   instance.Status.Conditions.Set(c)
}

// RbacNamespace - return the namespace
func (instance Designate) RbacNamespace() string {
   return instance.Namespace
}

// RbacResourceName - return the name to be used for rbac objects (serviceaccount, role, rolebinding)
func (instance Designate) RbacResourceName() string {
   return "designate-" + instance.Name
}
