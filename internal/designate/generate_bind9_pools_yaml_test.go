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

package designate

import (
	"strings"
	"testing"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
)

func TestValidateMultipoolConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *MultipoolConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with default pool",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						Description:  "Default Pool",
						BindReplicas: 2,
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns1.example.org.", Priority: 1},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid config with multiple pools",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						Description:  "Default Pool",
						BindReplicas: 2,
					},
					{
						Name:         "pool1",
						Description:  "Pool 1",
						BindReplicas: 3,
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing default pool",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "pool1",
						BindReplicas: 2,
					},
				},
			},
			wantErr: true,
			errMsg:  "default pool must be present",
		},
		{
			name: "empty pools",
			config: &MultipoolConfig{
				Pools: []PoolConfig{},
			},
			wantErr: true,
			errMsg:  "at least one pool must be defined",
		},
		{
			name: "duplicate pool names",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 2,
					},
					{
						Name:         "default",
						BindReplicas: 3,
					},
				},
			},
			wantErr: true,
			errMsg:  "duplicate pool name",
		},
		{
			name: "zero bind replicas",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 0,
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid bindReplicas",
		},
		{
			name: "negative bind replicas",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: -1,
					},
				},
			},
			wantErr: true,
			errMsg:  "invalid bindReplicas",
		},
		{
			name: "empty pool name",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "",
						BindReplicas: 2,
					},
				},
			},
			wantErr: true,
			errMsg:  "has empty name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMultipoolConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMultipoolConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validateMultipoolConfig() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestGenerateDefaultPool(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
		"bind_address_1": "192.168.1.11",
	}
	masterHosts := []string{"192.168.1.20", "192.168.1.21"}
	nsRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns1.example.org.", Priority: 1},
		{Hostname: "ns2.example.org.", Priority: 2},
	}

	pool, err := generateDefaultPool(bindMap, masterHosts, nsRecords)
	if err != nil {
		t.Fatalf("generateDefaultPool() error = %v", err)
	}

	if pool.Name != "default" {
		t.Errorf("expected pool name 'default', got %s", pool.Name)
	}

	if pool.Description != "Default BIND Pool" {
		t.Errorf("expected description 'Default BIND Pool', got %s", pool.Description)
	}

	if len(pool.Nameservers) != 2 {
		t.Errorf("expected 2 nameservers, got %d", len(pool.Nameservers))
	}

	if len(pool.Targets) != 2 {
		t.Errorf("expected 2 targets, got %d", len(pool.Targets))
	}

	if len(pool.NSRecords) != 2 {
		t.Errorf("expected 2 NS records, got %d", len(pool.NSRecords))
	}

	if pool.Nameservers[0].Host != "192.168.1.10" {
		t.Errorf("expected first nameserver host '192.168.1.10', got %s", pool.Nameservers[0].Host)
	}

	if pool.Targets[0].Options.RNDCKeyFile != "/etc/designate/rndc-keys/rndc-key-0" {
		t.Errorf("expected RNDC key file '/etc/designate/rndc-keys/rndc-key-0', got %s", pool.Targets[0].Options.RNDCKeyFile)
	}

	if len(pool.Targets[0].Masters) != 2 {
		t.Errorf("expected 2 masters in first target, got %d", len(pool.Targets[0].Masters))
	}
}

func TestGenerateMultiplePools(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
		"bind_address_1": "192.168.1.11",
		"bind_address_2": "192.168.1.12",
		"bind_address_3": "192.168.1.13",
	}
	masterHosts := []string{"192.168.1.20"}

	multipoolConfig := &MultipoolConfig{
		Pools: []PoolConfig{
			{
				Name:         "default",
				Description:  "Default Pool",
				BindReplicas: 2,
				Attributes:   map[string]string{"zone": "az1"},
				NSRecords: []designatev1.DesignateNSRecord{
					{Hostname: "ns1.example.org.", Priority: 1},
				},
			},
			{
				Name:         "pool1",
				Description:  "Pool 1",
				BindReplicas: 2,
				NSRecords: []designatev1.DesignateNSRecord{
					{Hostname: "ns2.example.org.", Priority: 1},
				},
			},
		},
	}

	pools, err := generateMultiplePools(bindMap, masterHosts, multipoolConfig)
	if err != nil {
		t.Fatalf("generateMultiplePools() error = %v", err)
	}

	if len(pools) != 2 {
		t.Fatalf("expected 2 pools, got %d", len(pools))
	}

	if pools[0].Name != "default" {
		t.Errorf("expected first pool name 'default', got %s", pools[0].Name)
	}

	if pools[1].Name != "pool1" {
		t.Errorf("expected second pool name 'pool1', got %s", pools[1].Name)
	}

	if len(pools[0].Nameservers) != 2 {
		t.Errorf("expected 2 nameservers in default pool, got %d", len(pools[0].Nameservers))
	}

	if len(pools[1].Nameservers) != 2 {
		t.Errorf("expected 2 nameservers in pool1, got %d", len(pools[1].Nameservers))
	}

	if pools[0].Nameservers[0].Host != "192.168.1.10" {
		t.Errorf("expected first nameserver in default pool to be '192.168.1.10', got %s", pools[0].Nameservers[0].Host)
	}

	if pools[1].Nameservers[0].Host != "192.168.1.12" {
		t.Errorf("expected first nameserver in pool1 to be '192.168.1.12', got %s", pools[1].Nameservers[0].Host)
	}

	if pools[0].Attributes["zone"] != "az1" {
		t.Errorf("expected attribute 'zone' to be 'az1', got %s", pools[0].Attributes["zone"])
	}

	if pools[0].NSRecords[0].Hostname != "ns1.example.org." {
		t.Errorf("expected NS record hostname 'ns1.example.org.', got %s", pools[0].NSRecords[0].Hostname)
	}

	if pools[1].NSRecords[0].Hostname != "ns2.example.org." {
		t.Errorf("expected NS record hostname 'ns2.example.org.', got %s", pools[1].NSRecords[0].Hostname)
	}

	if pools[0].Targets[0].Options.RNDCKeyFile != "/etc/designate/rndc-keys/rndc-key-0" {
		t.Errorf("expected RNDC key file '/etc/designate/rndc-keys/rndc-key-0', got %s", pools[0].Targets[0].Options.RNDCKeyFile)
	}

	if pools[1].Targets[0].Options.RNDCKeyFile != "/etc/designate/rndc-keys/rndc-key-2" {
		t.Errorf("expected RNDC key file '/etc/designate/rndc-keys/rndc-key-2', got %s", pools[1].Targets[0].Options.RNDCKeyFile)
	}
}

func TestMultipoolRequiresNSRecordsPerPool(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
		"bind_address_1": "192.168.1.11",
	}
	masterHosts := []string{"192.168.1.20"}

	// Test that multipool mode requires NS records for each pool
	multipoolConfigMissingNS := &MultipoolConfig{
		Pools: []PoolConfig{
			{
				Name:         "default",
				Description:  "Default Pool",
				BindReplicas: 1,
				NSRecords: []designatev1.DesignateNSRecord{
					{Hostname: "ns1.example.org.", Priority: 1},
				},
			},
			{
				Name:         "pool1",
				Description:  "Pool 1",
				BindReplicas: 1,
				// Missing NS records - should cause error
			},
		},
	}

	_, err := generateMultiplePools(bindMap, masterHosts, multipoolConfigMissingNS)
	if err == nil {
		t.Fatal("expected error when pool is missing NS records in multipool mode, got nil")
	}

	expectedErrMsg := "pool pool1 does not have NS records defined in multipool ConfigMap"
	if !strings.Contains(err.Error(), expectedErrMsg) {
		t.Errorf("expected error message to contain '%s', got: %s", expectedErrMsg, err.Error())
	}
}

func TestSinglePoolModeUsesCRNSRecords(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
		"bind_address_1": "192.168.1.11",
	}
	masterHosts := []string{"192.168.1.20"}

	crNSRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns-cr-single.example.org.", Priority: 1},
		{Hostname: "ns2-cr-single.example.org.", Priority: 2},
	}

	// In single-pool mode, generateDefaultPool should use CR NS records
	pool, err := generateDefaultPool(bindMap, masterHosts, crNSRecords)
	if err != nil {
		t.Fatalf("generateDefaultPool() error = %v", err)
	}

	if pool.Name != "default" {
		t.Errorf("expected pool name 'default', got %s", pool.Name)
	}

	if len(pool.NSRecords) != 2 {
		t.Fatalf("expected 2 NS records from CR, got %d", len(pool.NSRecords))
	}

	if pool.NSRecords[0].Hostname != "ns-cr-single.example.org." {
		t.Errorf("expected single-pool mode to use CR NS record 'ns-cr-single.example.org.', got %s", pool.NSRecords[0].Hostname)
	}

	if pool.NSRecords[1].Hostname != "ns2-cr-single.example.org." {
		t.Errorf("expected single-pool mode to use CR NS record 'ns2-cr-single.example.org.', got %s", pool.NSRecords[1].Hostname)
	}
}
