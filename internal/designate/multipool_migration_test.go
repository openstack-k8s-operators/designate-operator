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
	"testing"

	"gopkg.in/yaml.v3"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
)

func TestGenerateMultipoolConfigData(t *testing.T) {
	nsRecords := []designatev1.DesignateNSRecord{
		{Hostname: "ns1.example.org.", Priority: 1},
		{Hostname: "ns2.example.org.", Priority: 2},
	}

	data, err := GenerateMultipoolConfigData(3, nsRecords, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var pools []PoolConfig
	if err := yaml.Unmarshal([]byte(data), &pools); err != nil {
		t.Fatalf("failed to unmarshal translated config: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("expected one pool, got %d", len(pools))
	}

	pool := pools[0]
	if pool.Name != DefaultPoolName {
		t.Errorf("expected pool name %q, got %q", DefaultPoolName, pool.Name)
	}
	if pool.Description != DefaultPoolDescription {
		t.Errorf("expected pool description %q, got %q", DefaultPoolDescription, pool.Description)
	}
	if pool.BindReplicas != 3 {
		t.Errorf("expected 3 bind replicas, got %d", pool.BindReplicas)
	}
	if len(pool.NSRecords) != len(nsRecords) {
		t.Fatalf("expected %d NS records, got %d", len(nsRecords), len(pool.NSRecords))
	}
	for i, nsRecord := range nsRecords {
		if pool.NSRecords[i] != nsRecord {
			t.Errorf("expected NS record %+v, got %+v", nsRecord, pool.NSRecords[i])
		}
	}
}

func TestGenerateMultipoolConfigDataErrors(t *testing.T) {
	nsRecords := []designatev1.DesignateNSRecord{{Hostname: "ns1.example.org.", Priority: 1}}

	tests := []struct {
		name        string
		replicas    int32
		nsRecords   []designatev1.DesignateNSRecord
		azAwareMode designatev1.DesignateAZMode
		wantErr     error
	}{
		{
			name:      "missing NS records",
			replicas:  1,
			nsRecords: nil,
			wantErr:   ErrPoolMissingNSRecords,
		},
		{
			name:      "invalid bind replicas",
			replicas:  0,
			nsRecords: nsRecords,
			wantErr:   ErrInvalidBindReplicas,
		},
		{
			name:        "AZ-aware mode without a view",
			replicas:    1,
			nsRecords:   nsRecords,
			azAwareMode: designatev1.AZModeEnabled,
			wantErr:     ErrPoolMissingView,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := GenerateMultipoolConfigData(tt.replicas, tt.nsRecords, tt.azAwareMode)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}
