/*
Copyright 2026.

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
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
)

// ErrBindReplicasNotSet is returned when the CRD-based configuration has no
// backend bind replica count to translate into a pool.
var ErrBindReplicasNotSet = errors.New("designateBackendbind9.replicas is not set")

// GenerateMultipoolConfigData translates the legacy CRD-based single-pool
// configuration into the data used by the multipool ConfigMap. The translation
// produces a single default pool; the pool name, description, replica count and
// NS records are all derived from the Designate CR's existing configuration.
func GenerateMultipoolConfigData(
	bindReplicas int32,
	nsRecords []designatev1.DesignateNSRecord,
	azAwareMode designatev1.DesignateAZMode,
) (string, error) {
	if len(nsRecords) == 0 {
		return "", fmt.Errorf("%w: %s", ErrPoolMissingNSRecords, DefaultPoolName)
	}

	config := &MultipoolConfig{
		Pools: []PoolConfig{
			{
				Name:         DefaultPoolName,
				Description:  DefaultPoolDescription,
				BindReplicas: bindReplicas,
				NSRecords:    nsRecords,
			},
		},
		AZAwareMode: azAwareMode,
	}

	if err := validateMultipoolConfig(config, azAwareMode); err != nil {
		return "", fmt.Errorf("invalid translated multipool configuration: %w", err)
	}

	data, err := yaml.Marshal(config.Pools)
	if err != nil {
		return "", fmt.Errorf("failed to marshal translated multipool configuration: %w", err)
	}

	return string(data), nil
}
