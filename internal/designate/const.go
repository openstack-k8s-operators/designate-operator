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
)
