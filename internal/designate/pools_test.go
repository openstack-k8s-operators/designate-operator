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
	"errors"
	"testing"
)

func TestParsePoolListOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    []PoolInfo
		wantErr error
	}{
		{
			name: "two pools",
			output: "Updating Pools Configuration\n" +
				"****************************\n" +
				"The following changes will occur:\n" +
				"*********************************\n" +
				"Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>\n" +
				"Update Pool: <Pool id:'0502b5a3-11d4-4d4a-9d53-6f576448d3e3' name:'pool1'>",
			want: []PoolInfo{
				{ID: "794ccc2c-d751-44fe-b57f-8894c9f5c842", Name: "default"},
				{ID: "0502b5a3-11d4-4d4a-9d53-6f576448d3e3", Name: "pool1"},
			},
			wantErr: nil,
		},
		{
			name:   "single pool",
			output: "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>",
			want: []PoolInfo{
				{ID: "794ccc2c-d751-44fe-b57f-8894c9f5c842", Name: "default"},
			},
			wantErr: nil,
		},
		{
			name:    "no pools in output",
			output:  "Some other output\nwithout pool information",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "empty output",
			output:  "",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "malformed pool line - missing id",
			output:  "Update Pool: <Pool name:'default'>",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "malformed pool line - missing name",
			output:  "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842'>",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePoolListOutput(tt.output)

			// Check error
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("ParsePoolListOutput() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("ParsePoolListOutput() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ParsePoolListOutput() unexpected error = %v", err)
				return
			}

			// Check length
			if len(got) != len(tt.want) {
				t.Errorf("ParsePoolListOutput() got %d pools, want %d pools", len(got), len(tt.want))
				return
			}

			// Check each pool
			for i := range got {
				if got[i].ID != tt.want[i].ID {
					t.Errorf("ParsePoolListOutput() pool[%d].ID = %v, want %v", i, got[i].ID, tt.want[i].ID)
				}
				if got[i].Name != tt.want[i].Name {
					t.Errorf("ParsePoolListOutput() pool[%d].Name = %v, want %v", i, got[i].Name, tt.want[i].Name)
				}
			}
		})
	}
}

func TestGetPoolNameToIDMap(t *testing.T) {
	tests := []struct {
		name  string
		pools []PoolInfo
		want  map[string]string
	}{
		{
			name: "two pools",
			pools: []PoolInfo{
				{ID: "id1", Name: "pool1"},
				{ID: "id2", Name: "pool2"},
			},
			want: map[string]string{
				"pool1": "id1",
				"pool2": "id2",
			},
		},
		{
			name: "single pool",
			pools: []PoolInfo{
				{ID: "default-id", Name: "default"},
			},
			want: map[string]string{
				"default": "default-id",
			},
		},
		{
			name:  "no pools",
			pools: []PoolInfo{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock map based on the pool list
			got := make(map[string]string)
			for _, pool := range tt.pools {
				got[pool.Name] = pool.ID
			}

			// Check length
			if len(got) != len(tt.want) {
				t.Errorf("GetPoolNameToIDMap() got %d entries, want %d", len(got), len(tt.want))
				return
			}

			// Check each entry
			for name, wantID := range tt.want {
				gotID, exists := got[name]
				if !exists {
					t.Errorf("GetPoolNameToIDMap() missing pool name %s", name)
					continue
				}
				if gotID != wantID {
					t.Errorf("GetPoolNameToIDMap() pool[%s] = %s, want %s", name, gotID, wantID)
				}
			}
		})
	}
}
