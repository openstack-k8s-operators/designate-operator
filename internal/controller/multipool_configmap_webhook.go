/*
Copyright 2025.

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

package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var (
	// ErrPoolsKeyMissing is returned when the ConfigMap doesn't have a 'pools' key
	ErrPoolsKeyMissing = errors.New("ConfigMap must contain 'pools' key")
	// ErrNoPoolsDefined is returned when no pools are defined
	ErrNoPoolsDefined = errors.New("at least one pool must be defined")
	// ErrDefaultPoolMissing is returned when the default pool is not present
	ErrDefaultPoolMissing = errors.New("default pool 'default' must be present in multipool configuration")
	// ErrEmptyPoolName is returned when a pool has an empty name
	ErrEmptyPoolName = errors.New("pool has empty name")
	// ErrDuplicatePoolName is returned when duplicate pool names are found
	ErrDuplicatePoolName = errors.New("duplicate pool name")
	// ErrInvalidBindReplicas is returned when bindReplicas is invalid
	ErrInvalidBindReplicas = errors.New("pool has invalid bindReplicas (must be greater than 0)")
	// ErrPoolMissingNSRecords is returned when a pool is missing NS records
	ErrPoolMissingNSRecords = errors.New("pool does not have NS records defined (each pool must define nsRecords)")
)

// MultipoolConfigMapValidator validates the designate-multipool-config ConfigMap
type MultipoolConfigMapValidator struct {
	decoder *admission.Decoder
}

var multipoollog = logf.Log.WithName("multipool-configmap-webhook")

// PoolConfig represents a pool configuration from the ConfigMap
type PoolConfig struct {
	Name         string            `yaml:"name"`
	Description  string            `yaml:"description,omitempty"`
	Attributes   map[string]string `yaml:"attributes,omitempty"`
	BindReplicas int32             `yaml:"bindReplicas"`
	NSRecords    []NSRecord        `yaml:"nsRecords,omitempty"`
}

// NSRecord represents a nameserver record
type NSRecord struct {
	Hostname string `yaml:"hostname"`
	Priority int    `yaml:"priority"`
}

// Handle validates the ConfigMap
func (v *MultipoolConfigMapValidator) Handle(_ context.Context, req admission.Request) admission.Response {
	multipoollog.Info("Validating multipool ConfigMap", "name", req.Name, "namespace", req.Namespace)

	// Only validate the specific ConfigMap
	if req.Name != "designate-multipool-config" {
		return admission.Allowed("not the multipool config ConfigMap")
	}

	configMap := &corev1.ConfigMap{}
	err := (*v.decoder).Decode(req, configMap)
	if err != nil {
		return admission.Errored(http.StatusBadRequest, err)
	}

	// Validate the ConfigMap structure
	if err := v.validateConfigMap(configMap); err != nil {
		multipoollog.Error(err, "ConfigMap validation failed")
		return admission.Denied(err.Error())
	}

	return admission.Allowed("ConfigMap is valid")
}

// validateConfigMap validates the multipool configuration
// Note: Pool removal validation (checking for active zones) is handled by the controller
// This webhook only performs quick structural validation
func (v *MultipoolConfigMapValidator) validateConfigMap(cm *corev1.ConfigMap) error {
	poolsYaml, ok := cm.Data["pools"]
	if !ok {
		return ErrPoolsKeyMissing
	}

	var pools []PoolConfig
	if err := yaml.Unmarshal([]byte(poolsYaml), &pools); err != nil {
		return fmt.Errorf("failed to parse pools YAML: %w", err)
	}

	return validatePools(pools)
}

// validatePools validates the pool configuration
func validatePools(pools []PoolConfig) error {
	if len(pools) == 0 {
		return ErrNoPoolsDefined
	}

	poolNames := make(map[string]bool)
	hasDefault := false

	for i, pool := range pools {
		// Validate pool name is not empty (check first before using it)
		if pool.Name == "" {
			return fmt.Errorf("%w at index %d", ErrEmptyPoolName, i)
		}

		// Check for duplicate pool names
		if poolNames[pool.Name] {
			return fmt.Errorf("%w: %s", ErrDuplicatePoolName, pool.Name)
		}
		poolNames[pool.Name] = true

		// Check if default pool exists
		if pool.Name == "default" {
			hasDefault = true
		}

		// Validate bindReplicas must be > 0
		// Note: The operator can temporarily scale to 0 during graceful pool removal,
		// but users should not configure pools with 0 replicas (prevents pools.yaml generation errors)
		if pool.BindReplicas <= 0 {
			return fmt.Errorf("%w: pool %s has %d replicas", ErrInvalidBindReplicas, pool.Name, pool.BindReplicas)
		}

		// Validate NS records are provided for each pool
		// In multipool mode, each pool MUST have its own NS records (no fallback to CR)
		if len(pool.NSRecords) == 0 {
			return fmt.Errorf("%w: pool %s", ErrPoolMissingNSRecords, pool.Name)
		}
	}

	// Ensure default pool exists
	if !hasDefault {
		return ErrDefaultPoolMissing
	}

	return nil
}

// InjectDecoder injects the decoder
func (v *MultipoolConfigMapValidator) InjectDecoder(d *admission.Decoder) error {
	v.decoder = d
	return nil
}
