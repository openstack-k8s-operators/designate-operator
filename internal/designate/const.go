/*

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

package designate

const (
	// ServiceType -
	ServiceType = "dns"
	// ServiceName -
	ServiceName = "designate"

	// DatabaseName -
	DatabaseName = "designate"

	// DatabaseCRName - Name of the MariaDBDatabase CR
	DatabaseCRName = "designate"

	// DatabaseUsernamePrefix - used by EnsureMariaDBAccount when a new username
	// is to be generated, e.g. "designate_e5a4", "designate_78bc", etc
	DatabaseUsernamePrefix = "designate"

	// DesignatePublicPort -
	DesignatePublicPort int32 = 9001
	// DesignateInternalPort -
	DesignateInternalPort int32 = 9001

	// DesignateBindKeySecret is the name of the secret containing bind keys
	DesignateBindKeySecret = "designate-bind-secret"

	// DesignateRndcKey is the key name for RNDC configuration
	DesignateRndcKey = "rndc-key"

	// MdnsPredIPConfigMap is the name of the ConfigMap containing MDNS predictable IP mappings
	MdnsPredIPConfigMap = "designate-mdns-ip-map"

	// NsRecordsConfigMap is the name of the ConfigMap containing name server record parameters
	NsRecordsConfigMap = "designate-ns-records-params"

	// NsRecordsCRContent is the content key for name server records custom resource
	NsRecordsCRContent = "designate-ns-records"

	// BindPredIPConfigMap is the name of the ConfigMap containing bind predictable IP mappings
	BindPredIPConfigMap = "designate-bind-ip-map"

	// UnboundPredIPConfigMap is the name of the ConfigMap containing Unbound predictable IP mappings
	UnboundPredIPConfigMap = "designate-unbound-ip-map"

	// RndcConfDir is the directory path for RNDC configuration files
	RndcConfDir = "/etc/designate/rndc-keys"

	// PoolsYamlConfigMap is the name of the ConfigMap containing pools YAML configuration
	PoolsYamlConfigMap = "designate-pools-yaml-config-map"

	// PoolsYamlPath is the template path for pools YAML configuration
	PoolsYamlPath = "designatepoolmanager/config/pools.yaml.tmpl"

	// PoolsYamlHash is the hash key for pools YAML configuration
	PoolsYamlHash = "pools-yaml-hash"

	// PoolsYamlContent is the content key for pools YAML configuration
	PoolsYamlContent = "pools-yaml-content"

	// BindPredictableIPHash key for status hash
	BindPredictableIPHash = "Bind IP Map"

	// RndcHash key for status hash
	RndcHash = "Rndc keys"

	// PredictableIPCommand -
	PredictableIPCommand = "/usr/local/bin/container-scripts/setipalias.sh"

	// ScriptsF is the sprintf template for common scripts secret
	ScriptsF = "%s-scripts"

	// ConfigDataTemplate sprintf template for common config data secret
	ConfigDataTemplate = "%s-config-data"

	// DefaultOverwriteTemplate sprintf template for common default overwrite secret
	DefaultOverwriteTemplate = "%s-defaults"

	// TsigSecretSuffix is the suffix for TSIG secret names in multipool mode
	TsigSecretSuffix = "-tsig"

	// MultipoolConfigMapName is the name of the ConfigMap containing multipool configuration
	MultipoolConfigMapName = "designate-multipool-config"

	// DefaultPoolName is the name of the default pool (pool0)
	DefaultPoolName = "default"

	// PoolStatefulSetSuffix is the suffix for numbered pool StatefulSets (pool1, pool2, etc.)
	PoolStatefulSetSuffix = "-pool"

	// SharedTSIGKeyName is the name of the shared TSIG key used for all non-default pools
	SharedTSIGKeyName = "multipool-shared-key"

	// PerPoolTSIGKeySuffix is the suffix appended to pool names for per-pool TSIG keys
	PerPoolTSIGKeySuffix = "-tsig-key"

	// TSIGKeyIDsDataKey is the key in the TSIG Secret that stores the pool-name to tsigkey-id JSON mapping
	TSIGKeyIDsDataKey = "tsigkey-ids.json"

	// ACConsumerFinalizer is added to AC secrets that designate is actively consuming
	ACConsumerFinalizer = "openstack.org/designateapi-ac-consumer"

	// ExternalBindsData is the name of the secret containing external BIND9 configurations
	ExternalBindsData = "designate-external-binds"

	// ExternalRndcData is the name of the secret for mounting the external binds rndc keys
	// in the workers.
	ExternalRndcData = "designate-external-rndc"

	// Fields for Service objects for accessing mDNS/DNS masters from outside the cluster for
	// external DNS secondaries. Why a label AND an annotation? The label is used to identify the
	// service as an external master, while the annotation is used to specify the pool name the
	// external service is intended for. This allows different pools to have different masters
	// and be configured with different networking as needed.

	// ExternalMasterServiceLabel is the label name for identifying services to be as mDNS
	// endpoints
	ExternalMasterServiceLabel = "designate.openstack.org/external_master"

	// ExternalPoolServiceAnnotation is the annotation name for specifying the pool name the external
	// service is intended for
	ExternalPoolServiceAnnotation = "designate.openstack.org/external_pool"
)
