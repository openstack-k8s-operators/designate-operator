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

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"text/template"

	"gopkg.in/yaml.v2"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrNoPoolsDefined is returned when no pools are defined in multipool configuration
	ErrNoPoolsDefined = errors.New("at least one pool must be defined")
	// ErrDuplicatePoolName is returned when duplicate pool names are found
	ErrDuplicatePoolName = errors.New("duplicate pool name")
	// ErrEmptyPoolName is returned when a pool has an empty name
	ErrEmptyPoolName = errors.New("pool has empty name")
	// ErrInvalidBindReplicas is returned when pool has invalid bindReplicas value
	ErrInvalidBindReplicas = errors.New("pool has invalid bindReplicas (must be greater than 0)")
	// ErrDefaultPoolMissing is returned when default pool is not present in multipool configuration
	ErrDefaultPoolMissing = errors.New("default pool must be present in multipool configuration")
	// ErrMultipoolConfigKeyMissing is returned when multipool ConfigMap doesn't have pools key
	ErrMultipoolConfigKeyMissing = errors.New("multipool ConfigMap missing pools key")
	// ErrPoolMissingNSRecords is returned when a pool doesn't define NS records in multipool mode
	ErrPoolMissingNSRecords = errors.New("pool missing NS records in multipool mode")
)

const (
	// MultipoolConfigMapKey is the key in the ConfigMap containing the pools configuration
	MultipoolConfigMapKey = "pools"
	// bindAddressKeyTemplate is the template for bind address keys in ConfigMaps
	bindAddressKeyTemplate = "bind_address_%d"

	// MdnsMasterPort is the port used by mDNS masters
	MdnsMasterPort = 5354
	// DNSPort is the standard DNS port used by BIND9 nameservers and targets
	DNSPort = 53
	// RNDCPort is the port used by RNDC for BIND9 control operations
	RNDCPort = 953
)

// Pool represents a designate pool configuration
type Pool struct {
	Name        string                          `yaml:"name"`
	Description string                          `yaml:"description"`
	Attributes  map[string]string               `yaml:"attributes"`
	NSRecords   []designatev1.DesignateNSRecord `yaml:"ns_records"`
	Nameservers []Nameserver                    `yaml:"nameservers"`
	Targets     []Target                        `yaml:"targets"`
	CatalogZone *CatalogZone                    `yaml:"catalog_zone,omitempty"` // it is a pointer because it is optional
}

// We have the same defined in the API now
// type NSRecord struct {
// 	Hostname string `yaml:"hostname"`
// 	Priority int    `yaml:"priority"`
// }

// Nameserver represents a nameserver configuration
type Nameserver struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Target represents a designate target configuration
type Target struct {
	Type        string   `yaml:"type"`
	Description string   `yaml:"description"`
	Masters     []Master `yaml:"masters"`
	Options     Options  `yaml:"options"`
}

// Master represents a designate master configuration
type Master struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// Options represents designate target options
type Options struct {
	Host        string `yaml:"host"`
	Port        int    `yaml:"port"`
	RNDCHost    string `yaml:"rndc_host"`
	RNDCPort    int    `yaml:"rndc_port"`
	RNDCKeyFile string `yaml:"rndc_key_file"`
}

// CatalogZone represents a designate catalog zone configuration
type CatalogZone struct {
	FQDN    string `yaml:"fqdn"`
	Refresh int    `yaml:"refresh"`
}

// PoolConfig defines configuration for a single pool from the multipool ConfigMap
type PoolConfig struct {
	Name         string                          `yaml:"name"`
	Description  string                          `yaml:"description,omitempty"`
	Attributes   map[string]string               `yaml:"attributes,omitempty"`
	BindReplicas int32                           `yaml:"bindReplicas"`
	NSRecords    []designatev1.DesignateNSRecord `yaml:"nsRecords,omitempty"`
}

// MultipoolConfig defines the complete multipool configuration
type MultipoolConfig struct {
	Pools []PoolConfig
}

// GetMultipoolConfig reads and parses the multipool ConfigMap
// Returns nil if ConfigMap doesn't exist
func GetMultipoolConfig(ctx context.Context, k8sClient client.Client, namespace string) (*MultipoolConfig, error) {
	configMap := &corev1.ConfigMap{}
	err := k8sClient.Get(ctx, types.NamespacedName{
		Name:      MultipoolConfigMapName,
		Namespace: namespace,
	}, configMap)

	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}

	poolsYaml, ok := configMap.Data[MultipoolConfigMapKey]
	if !ok {
		return nil, fmt.Errorf("%w: %s/%s", ErrMultipoolConfigKeyMissing, MultipoolConfigMapName, MultipoolConfigMapKey)
	}

	var pools []PoolConfig
	if err := yaml.Unmarshal([]byte(poolsYaml), &pools); err != nil {
		return nil, fmt.Errorf("failed to parse multipool config: %w", err)
	}

	// Sort pools by name for stable ordering
	sort.Slice(pools, func(i, j int) bool {
		return pools[i].Name < pools[j].Name
	})

	config := &MultipoolConfig{Pools: pools}

	if err := validateMultipoolConfig(config); err != nil {
		return nil, fmt.Errorf("invalid multipool config: %w", err)
	}

	return config, nil
}

// validateMultipoolConfig validates the multipool configuration
func validateMultipoolConfig(config *MultipoolConfig) error {
	if len(config.Pools) == 0 {
		return ErrNoPoolsDefined
	}

	poolNames := make(map[string]bool)
	hasDefault := false

	for i, pool := range config.Pools {
		// Check for duplicate pool names
		if poolNames[pool.Name] {
			return fmt.Errorf("%w: %s", ErrDuplicatePoolName, pool.Name)
		}
		poolNames[pool.Name] = true

		// Check if default pool exists
		if pool.Name == DefaultPoolName {
			hasDefault = true
		}

		// Validate pool name is not empty
		if pool.Name == "" {
			return fmt.Errorf("%w at index %d", ErrEmptyPoolName, i)
		}

		// Validate bindReplicas must be greater than 0
		if pool.BindReplicas <= 0 {
			return fmt.Errorf("%w: pool %s has %d", ErrInvalidBindReplicas, pool.Name, pool.BindReplicas)
		}
	}

	// Ensure default pool exists and cannot be removed
	if !hasDefault {
		return fmt.Errorf("%w: '%s'", ErrDefaultPoolMissing, DefaultPoolName)
	}

	return nil
}

// GeneratePoolsYamlDataAndHash sorts all pool resources to get the correct hash every time
func GeneratePoolsYamlDataAndHash(BindMap, MdnsMap map[string]string, nsRecords []designatev1.DesignateNSRecord, multipoolConfig *MultipoolConfig) (string, string, error) {
	masterHosts := make([]string, 0, len(MdnsMap))
	for _, host := range MdnsMap {
		masterHosts = append(masterHosts, host)
	}
	sort.Strings(masterHosts)

	var pools []Pool

	if multipoolConfig == nil {
		pool, err := generateDefaultPool(BindMap, masterHosts, nsRecords)
		if err != nil {
			return "", "", err
		}
		pools = []Pool{pool}
	} else {
		var err error
		pools, err = generateMultiplePools(BindMap, masterHosts, multipoolConfig)
		if err != nil {
			return "", "", err
		}
	}

	poolBytes, err := yaml.Marshal(pools)
	if err != nil {
		return "", "", fmt.Errorf("error marshalling pools for hash: %w", err)
	}
	hasher := sha256.New()
	hasher.Write(poolBytes)
	poolHash := hex.EncodeToString(hasher.Sum(nil))

	opTemplates, err := util.GetTemplatesPath()
	if err != nil {
		return "", "", err
	}
	poolsYamlPath := path.Join(opTemplates, PoolsYamlPath)
	poolsYaml, err := os.ReadFile(poolsYamlPath) // #nosec G304
	if err != nil {
		return "", "", err
	}
	tmpl, err := template.New("pools").Parse(string(poolsYaml))
	if err != nil {
		return "", "", err
	}
	var buf bytes.Buffer
	err = tmpl.Execute(&buf, pools)
	if err != nil {
		return "", "", err
	}

	return buf.String(), poolHash, nil
}

// sortNSRecords sorts NS records by hostname and then by priority
func sortNSRecords(nsRecords []designatev1.DesignateNSRecord) {
	sort.Slice(nsRecords, func(i, j int) bool {
		if nsRecords[i].Hostname != nsRecords[j].Hostname {
			return nsRecords[i].Hostname < nsRecords[j].Hostname
		}
		return nsRecords[i].Priority < nsRecords[j].Priority
	})
}

// createMasters creates a list of Master structs from master host addresses
func createMasters(masterHosts []string) []Master {
	masters := make([]Master, len(masterHosts))
	for j, masterHost := range masterHosts {
		masters[j] = Master{
			Host: masterHost,
			Port: MdnsMasterPort,
		}
	}
	return masters
}

// createTargetAndNameserver creates Target and Nameserver structs for a bind instance
func createTargetAndNameserver(bindIP string, globalIndex int, masters []Master, description string) (Target, Nameserver) {
	nameserver := Nameserver{
		Host: bindIP,
		Port: DNSPort,
	}
	target := Target{
		Type:        "bind9",
		Description: description,
		Masters:     masters,
		Options: Options{
			Host:        bindIP,
			Port:        DNSPort,
			RNDCHost:    bindIP,
			RNDCPort:    RNDCPort,
			RNDCKeyFile: fmt.Sprintf("%s/%s-%d", RndcConfDir, DesignateRndcKey, globalIndex),
		},
	}
	return target, nameserver
}

func generateDefaultPool(BindMap map[string]string, masterHosts []string, nsRecords []designatev1.DesignateNSRecord) (Pool, error) {
	sortNSRecords(nsRecords)

	bindIPs := make([]string, len(BindMap))
	for i := 0; i < len(BindMap); i++ {
		bindIPs[i] = BindMap[fmt.Sprintf(bindAddressKeyTemplate, i)]
	}

	masters := createMasters(masterHosts)
	targets := make([]Target, len(bindIPs))
	nameservers := make([]Nameserver, len(bindIPs))
	for i := range bindIPs {
		description := fmt.Sprintf("BIND9 Server %d (%s)", i, bindIPs[i])
		targets[i], nameservers[i] = createTargetAndNameserver(bindIPs[i], i, masters, description)
	}

	// Catalog zone is an optional section
	// catalogZone := &CatalogZone{
	// 	FQDN:    "example.org.",
	//	Refresh: 60,
	// }
	defaultAttributes := make(map[string]string)
	pool := Pool{
		Name:        "default",
		Description: "Default BIND Pool",
		Attributes:  defaultAttributes,
		NSRecords:   nsRecords,
		Nameservers: nameservers,
		Targets:     targets,
		CatalogZone: nil, // set to catalogZone if this section should be presented
	}

	return pool, nil
}

func generateMultiplePools(BindMap map[string]string, masterHosts []string, multipoolConfig *MultipoolConfig) ([]Pool, error) {
	// Sort pools by name for stable ordering
	sortedPools := make([]PoolConfig, len(multipoolConfig.Pools))
	copy(sortedPools, multipoolConfig.Pools)
	sort.Slice(sortedPools, func(i, j int) bool {
		return sortedPools[i].Name < sortedPools[j].Name
	})

	pools := make([]Pool, 0, len(sortedPools))
	bindIndex := 0

	for _, poolConfig := range sortedPools {
		// In multipool mode, each pool MUST define its own NS records
		if len(poolConfig.NSRecords) == 0 {
			return nil, fmt.Errorf("%w: %s", ErrPoolMissingNSRecords, poolConfig.Name)
		}

		nsRecords := poolConfig.NSRecords
		sortNSRecords(nsRecords)

		// Extract this pool's bind IPs from the global BindMap.
		// Each pool has its own set of replicas with unique IPs allocated from the global IP pool.
		// The bindIndex tracks our position in the global IP allocation across all pools.
		// Example: Pool0 gets IPs 0-1, Pool1 gets IPs 2-3, etc.
		poolBindIPs := make([]string, poolConfig.BindReplicas)
		for i := int32(0); i < poolConfig.BindReplicas; i++ {
			poolBindIPs[i] = BindMap[fmt.Sprintf(bindAddressKeyTemplate, bindIndex)]
			bindIndex++
		}

		masters := createMasters(masterHosts)
		targets := make([]Target, poolConfig.BindReplicas)
		nameservers := make([]Nameserver, poolConfig.BindReplicas)
		for i := int32(0); i < poolConfig.BindReplicas; i++ {
			globalIndex := bindIndex - int(poolConfig.BindReplicas) + int(i)
			description := fmt.Sprintf("BIND9 Server for pool %s (%s)", poolConfig.Name, poolBindIPs[i])
			targets[i], nameservers[i] = createTargetAndNameserver(poolBindIPs[i], globalIndex, masters, description)
		}

		attributes := poolConfig.Attributes
		if attributes == nil {
			attributes = make(map[string]string)
		}

		// Catalog zone is an optional section
		// catalogZone := &CatalogZone{
		// 	FQDN:    "example.org.",
		//	Refresh: 60,
		// }
		pool := Pool{
			Name:        poolConfig.Name,
			Description: poolConfig.Description,
			Attributes:  attributes,
			NSRecords:   nsRecords,
			Nameservers: nameservers,
			Targets:     targets,
			CatalogZone: nil, // set to catalogZone if this section should be presented
		}

		pools = append(pools, pool)
	}

	return pools, nil
}
