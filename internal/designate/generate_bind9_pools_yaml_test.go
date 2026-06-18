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
			err := validateMultipoolConfig(tt.config, "")
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

func TestValidateMultipoolConfigAZMode(t *testing.T) {
	tests := []struct {
		name    string
		config  *MultipoolConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid AZ config with views on all pools",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 2,
						View:         "default",
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns1.example.org.", Priority: 1},
						},
					},
					{
						Name:         "pool1",
						BindReplicas: 2,
						View:         "az1-view",
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns2.example.org.", Priority: 1},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "multiple pools sharing same view is valid",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 2,
						View:         "shared-view",
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns1.example.org.", Priority: 1},
						},
					},
					{
						Name:         "pool1",
						BindReplicas: 2,
						View:         "shared-view",
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns2.example.org.", Priority: 1},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "reject pool missing view in AZ mode",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 2,
						View:         "default",
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns1.example.org.", Priority: 1},
						},
					},
					{
						Name:         "pool1",
						BindReplicas: 2,
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns2.example.org.", Priority: 1},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "pool missing view in AZ-aware mode",
		},
		{
			name: "reject default pool missing view in AZ mode",
			config: &MultipoolConfig{
				Pools: []PoolConfig{
					{
						Name:         "default",
						BindReplicas: 2,
						NSRecords: []designatev1.DesignateNSRecord{
							{Hostname: "ns1.example.org.", Priority: 1},
						},
					},
				},
			},
			wantErr: true,
			errMsg:  "pool missing view in AZ-aware mode: pool default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMultipoolConfig(tt.config, designatev1.AZModeEnabled)
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

	pool, err := generateDefaultPool(bindMap, masterHosts, nsRecords, []ExternalBind{})
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

	expectedErrMsg := "pool missing NS records in multipool mode"
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
	pool, err := generateDefaultPool(bindMap, masterHosts, crNSRecords, []ExternalBind{})
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

func TestCreateExternalTargetAndNameserver(t *testing.T) {
	masters := []Master{{Host: "192.168.1.20", Port: MdnsMasterPort}}

	tests := []struct {
		name            string
		bindInfo        ExternalBind
		description     string
		wantHost        string
		wantPort        int
		wantRndcHost    string
		wantRndcPort    int
		wantRndcKeyFile string
	}{
		{
			name: "minimal external bind with defaults",
			bindInfo: ExternalBind{
				Name:     "bind9-external",
				Address:  "192.168.100.50",
				Port:     DNSPort,
				RndcHost: "192.168.100.50",
				RndcPort: RNDCPort,
				RndcFile: "default-rndc-0",
			},
			description:     "External BIND9 Server 0 (bind9-external)",
			wantHost:        "192.168.100.50",
			wantPort:        DNSPort,
			wantRndcHost:    "192.168.100.50",
			wantRndcPort:    RNDCPort,
			wantRndcKeyFile: "/etc/designate/rndc-keys/default-rndc-0",
		},
		{
			name: "custom ports and separate rndc host",
			bindInfo: ExternalBind{
				Name:     "bind9-primary",
				Address:  "192.168.1.10",
				Port:     5353,
				RndcHost: "10.0.0.99",
				RndcPort: 1953,
				RndcFile: "default-rndc-1",
			},
			description:     "External BIND9 Server 1 (bind9-primary)",
			wantHost:        "192.168.1.10",
			wantPort:        5353,
			wantRndcHost:    "10.0.0.99",
			wantRndcPort:    1953,
			wantRndcKeyFile: "/etc/designate/rndc-keys/default-rndc-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target, nameserver := createExternalTargetAndNameserver(tt.bindInfo, masters, tt.description)

			if target.Type != "bind9" {
				t.Errorf("expected target type 'bind9', got %s", target.Type)
			}
			if target.Description != tt.description {
				t.Errorf("expected description %q, got %q", tt.description, target.Description)
			}
			if target.TSIGKeyID != "" {
				t.Errorf("expected empty TSIGKeyID for external target, got %q", target.TSIGKeyID)
			}
			if len(target.Masters) != 1 {
				t.Fatalf("expected 1 master, got %d", len(target.Masters))
			}
			if target.Masters[0].Host != "192.168.1.20" {
				t.Errorf("expected master host '192.168.1.20', got %s", target.Masters[0].Host)
			}
			if target.Masters[0].Port != MdnsMasterPort {
				t.Errorf("expected master port %d, got %d", MdnsMasterPort, target.Masters[0].Port)
			}
			if target.Options.Host != tt.wantHost {
				t.Errorf("expected options host %q, got %q", tt.wantHost, target.Options.Host)
			}
			if target.Options.Port != tt.wantPort {
				t.Errorf("expected options port %d, got %d", tt.wantPort, target.Options.Port)
			}
			if target.Options.RNDCHost != tt.wantRndcHost {
				t.Errorf("expected RNDC host %q, got %q", tt.wantRndcHost, target.Options.RNDCHost)
			}
			if target.Options.RNDCPort != tt.wantRndcPort {
				t.Errorf("expected RNDC port %d, got %d", tt.wantRndcPort, target.Options.RNDCPort)
			}
			if target.Options.RNDCKeyFile != tt.wantRndcKeyFile {
				t.Errorf("expected RNDC key file %q, got %q", tt.wantRndcKeyFile, target.Options.RNDCKeyFile)
			}
			if target.Options.View != "" {
				t.Errorf("expected empty view for external target, got %q", target.Options.View)
			}

			if nameserver.Host != tt.wantHost {
				t.Errorf("expected nameserver host %q, got %q", tt.wantHost, nameserver.Host)
			}
			if nameserver.Port != tt.wantPort {
				t.Errorf("expected nameserver port %d, got %d", tt.wantPort, nameserver.Port)
			}
			if nameserver.TSIGKeyID != "" {
				t.Errorf("expected empty TSIGKeyID for external nameserver, got %q", nameserver.TSIGKeyID)
			}
		})
	}
}

func TestGenerateDefaultPoolWithExternalBinds(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
		"bind_address_1": "192.168.1.11",
	}
	masterHosts := []string{"192.168.1.20", "192.168.1.21"}
	nsRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns1.example.org.", Priority: 1},
	}
	externalBinds := []ExternalBind{
		{
			Name:     "bind9-external",
			Address:  "192.168.100.50",
			Port:     DNSPort,
			RndcHost: "192.168.100.50",
			RndcPort: RNDCPort,
			RndcFile: "default-rndc-0",
		},
	}

	pool, err := generateDefaultPool(bindMap, masterHosts, nsRecords, externalBinds)
	if err != nil {
		t.Fatalf("generateDefaultPool() error = %v", err)
	}

	if len(pool.Targets) != 3 {
		t.Fatalf("expected 3 targets (2 internal + 1 external), got %d", len(pool.Targets))
	}
	if len(pool.Nameservers) != 3 {
		t.Fatalf("expected 3 nameservers (2 internal + 1 external), got %d", len(pool.Nameservers))
	}

	internalTarget := pool.Targets[0]
	if internalTarget.Options.Host != "192.168.1.10" {
		t.Errorf("expected first internal target host '192.168.1.10', got %s", internalTarget.Options.Host)
	}
	if internalTarget.Options.RNDCKeyFile != "/etc/designate/rndc-keys/rndc-key-0" {
		t.Errorf("expected internal RNDC key file '/etc/designate/rndc-keys/rndc-key-0', got %s", internalTarget.Options.RNDCKeyFile)
	}

	externalTarget := pool.Targets[2]
	if externalTarget.Description != "External BIND9 Server 0 (bind9-external)" {
		t.Errorf("expected external target description 'External BIND9 Server 0 (bind9-external)', got %s", externalTarget.Description)
	}
	if externalTarget.Options.Host != "192.168.100.50" {
		t.Errorf("expected external target host '192.168.100.50', got %s", externalTarget.Options.Host)
	}
	if externalTarget.Options.RNDCHost != "192.168.100.50" {
		t.Errorf("expected external RNDC host '192.168.100.50', got %s", externalTarget.Options.RNDCHost)
	}
	if externalTarget.Options.RNDCKeyFile != "/etc/designate/rndc-keys/default-rndc-0" {
		t.Errorf("expected external RNDC key file '/etc/designate/rndc-keys/default-rndc-0', got %s", externalTarget.Options.RNDCKeyFile)
	}
	if len(externalTarget.Masters) != 2 {
		t.Errorf("expected 2 masters on external target, got %d", len(externalTarget.Masters))
	}

	externalNameserver := pool.Nameservers[2]
	if externalNameserver.Host != "192.168.100.50" {
		t.Errorf("expected external nameserver host '192.168.100.50', got %s", externalNameserver.Host)
	}
	if externalNameserver.Port != DNSPort {
		t.Errorf("expected external nameserver port %d, got %d", DNSPort, externalNameserver.Port)
	}
}

func TestGenerateDefaultPoolWithMultipleExternalBinds(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
	}
	masterHosts := []string{"192.168.1.20"}
	nsRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns1.example.org.", Priority: 1},
	}
	externalBinds := []ExternalBind{
		{
			Name:     "bind9-external-1",
			Address:  "192.168.100.50",
			Port:     DNSPort,
			RndcHost: "192.168.100.50",
			RndcPort: RNDCPort,
			RndcFile: "default-rndc-0",
		},
		{
			Name:     "bind9-external-2",
			Address:  "192.168.100.51",
			Port:     5353,
			RndcHost: "10.0.0.99",
			RndcPort: 1953,
			RndcFile: "default-rndc-1",
		},
	}

	pool, err := generateDefaultPool(bindMap, masterHosts, nsRecords, externalBinds)
	if err != nil {
		t.Fatalf("generateDefaultPool() error = %v", err)
	}

	if len(pool.Targets) != 3 {
		t.Fatalf("expected 3 targets (1 internal + 2 external), got %d", len(pool.Targets))
	}

	firstExternal := pool.Targets[1]
	if firstExternal.Description != "External BIND9 Server 0 (bind9-external-1)" {
		t.Errorf("expected first external description 'External BIND9 Server 0 (bind9-external-1)', got %s", firstExternal.Description)
	}
	if firstExternal.Options.Host != "192.168.100.50" {
		t.Errorf("expected first external host '192.168.100.50', got %s", firstExternal.Options.Host)
	}

	secondExternal := pool.Targets[2]
	if secondExternal.Description != "External BIND9 Server 1 (bind9-external-2)" {
		t.Errorf("expected second external description 'External BIND9 Server 1 (bind9-external-2)', got %s", secondExternal.Description)
	}
	if secondExternal.Options.Host != "192.168.100.51" {
		t.Errorf("expected second external host '192.168.100.51', got %s", secondExternal.Options.Host)
	}
	if secondExternal.Options.Port != 5353 {
		t.Errorf("expected second external port 5353, got %d", secondExternal.Options.Port)
	}
	if secondExternal.Options.RNDCHost != "10.0.0.99" {
		t.Errorf("expected second external RNDC host '10.0.0.99', got %s", secondExternal.Options.RNDCHost)
	}
	if secondExternal.Options.RNDCPort != 1953 {
		t.Errorf("expected second external RNDC port 1953, got %d", secondExternal.Options.RNDCPort)
	}
	if secondExternal.Options.RNDCKeyFile != "/etc/designate/rndc-keys/default-rndc-1" {
		t.Errorf("expected second external RNDC key file '/etc/designate/rndc-keys/default-rndc-1', got %s", secondExternal.Options.RNDCKeyFile)
	}
}

func TestGenerateDefaultPoolWithExternalBindsFromYAML(t *testing.T) {
	bindMap := map[string]string{
		"bind_address_0": "192.168.1.10",
	}
	masterHosts := []string{"192.168.1.20"}
	nsRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns1.example.org.", Priority: 1},
	}

	externalBindsYAML := `
- name: bind9-external
  address: 192.168.100.50
  rndcsecret: c2VjcmV0MTIz
`
	parsedBinds, err := ReadExternalBinds([]byte(externalBindsYAML))
	if err != nil {
		t.Fatalf("ReadExternalBinds() error = %v", err)
	}
	parsedBinds[0].RndcFile = "default-rndc-0"

	pool, err := generateDefaultPool(bindMap, masterHosts, nsRecords, parsedBinds)
	if err != nil {
		t.Fatalf("generateDefaultPool() error = %v", err)
	}

	if len(pool.Targets) != 2 {
		t.Fatalf("expected 2 targets (1 internal + 1 external), got %d", len(pool.Targets))
	}

	externalTarget := pool.Targets[1]
	if externalTarget.Options.Host != "192.168.100.50" {
		t.Errorf("expected external target host '192.168.100.50', got %s", externalTarget.Options.Host)
	}
	if externalTarget.Options.Port != DNSPort {
		t.Errorf("expected external target port %d, got %d", DNSPort, externalTarget.Options.Port)
	}
	if externalTarget.Options.RNDCHost != "192.168.100.50" {
		t.Errorf("expected external RNDC host defaulted to address, got %s", externalTarget.Options.RNDCHost)
	}
	if externalTarget.Options.RNDCPort != RNDCPort {
		t.Errorf("expected external RNDC port %d, got %d", RNDCPort, externalTarget.Options.RNDCPort)
	}
}
