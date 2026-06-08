/*
Copyright 2024.

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
	"testing"
)

func TestReadExternalBinds(t *testing.T) {
	rndcHost := "10.0.0.99"

	tests := []struct {
		name        string
		input       string
		expected    []ExternalBind
		expectError bool
	}{
		{
			name: "all fields specified",
			input: `
- name: bind9-primary
  address: 192.168.1.10
  port: 5353
  rndchost: 10.0.0.99
  rndckeyname: my-custom-key
  rndcalgorithm: hmac-sha512
  rndcsecret: c2VjcmV0MTIz
  rndcport: 1953
`,
			expected: []ExternalBind{
				{ //nolint:gosec // G101: test fixture, not a real credential
					Name:          "bind9-primary",
					Address:       "192.168.1.10",
					Port:          5353,
					RndcHost:      rndcHost,
					RndcKeyName:   "my-custom-key",
					RndcAlgorithm: "hmac-sha512",
					RndcSecret:    "c2VjcmV0MTIz",
					RndcPort:      1953,
				},
			},
		},
		{
			name: "defaults applied for omitted fields",
			input: `
- name: bind9-minimal
  address: 192.168.1.10
  rndcsecret: c2VjcmV0MTIz
`,
			expected: []ExternalBind{
				{ //nolint:gosec // G101: test fixture, not a real credential
					Name:          "bind9-minimal",
					Address:       "192.168.1.10",
					Port:          DNSPort,
					RndcHost:      "192.168.1.10",
					RndcKeyName:   DefaultRndcKeyName,
					RndcAlgorithm: DefaultRndcAlgorithm,
					RndcSecret:    "c2VjcmV0MTIz",
					RndcPort:      RNDCPort,
				},
			},
		},
		{
			name: "multiple entries with mixed defaults",
			input: `
- name: primary
  address: 192.168.1.10
  port: 5353
  rndcsecret: c2VjcmV0MQ==
- name: secondary
  address: 192.168.1.11
  rndchost: 10.0.0.99
  rndckeyname: custom-key
  rndcsecret: c2VjcmV0Mg==
`,
			expected: []ExternalBind{
				{ //nolint:gosec // G101: test fixture, not a real credential
					Name:          "primary",
					Address:       "192.168.1.10",
					Port:          5353,
					RndcHost:      "192.168.1.10",
					RndcKeyName:   DefaultRndcKeyName,
					RndcAlgorithm: DefaultRndcAlgorithm,
					RndcSecret:    "c2VjcmV0MQ==",
					RndcPort:      RNDCPort,
				},
				{ //nolint:gosec // G101: test fixture, not a real credential
					Name:          "secondary",
					Address:       "192.168.1.11",
					Port:          DNSPort,
					RndcHost:      rndcHost,
					RndcKeyName:   "custom-key",
					RndcAlgorithm: DefaultRndcAlgorithm,
					RndcSecret:    "c2VjcmV0Mg==",
					RndcPort:      RNDCPort,
				},
			},
		},
		{
			name:     "empty input",
			input:    "",
			expected: []ExternalBind{},
		},
		{
			name:     "empty array",
			input:    "[]",
			expected: []ExternalBind{},
		},
		{
			name:        "invalid yaml",
			input:       "not: valid: yaml: [list",
			expectError: true,
		},
		{
			name:        "wrong type - object instead of array",
			input:       "name: single\naddress: 192.168.1.10\n",
			expectError: true,
		},
		{
			name: "zero port treated as default",
			input: `
- name: bind9-zero-port
  address: 192.168.1.10
  port: 0
  rndcport: 0
  rndcsecret: c2VjcmV0
`,
			expected: []ExternalBind{
				{ //nolint:gosec // G101: test fixture, not a real credential
					Name:          "bind9-zero-port",
					Address:       "192.168.1.10",
					Port:          DNSPort,
					RndcHost:      "192.168.1.10",
					RndcKeyName:   DefaultRndcKeyName,
					RndcAlgorithm: DefaultRndcAlgorithm,
					RndcSecret:    "c2VjcmV0",
					RndcPort:      RNDCPort,
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ReadExternalBinds([]byte(tc.input))

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(result) != len(tc.expected) {
				t.Fatalf("expected %d entries, got %d", len(tc.expected), len(result))
			}

			for i, got := range result {
				exp := tc.expected[i]
				if got.Name != exp.Name {
					t.Errorf("[%d] Name: got %q, want %q", i, got.Name, exp.Name)
				}
				if got.Address != exp.Address {
					t.Errorf("[%d] Address: got %q, want %q", i, got.Address, exp.Address)
				}
				if got.Port != exp.Port {
					t.Errorf("[%d] Port: got %d, want %d", i, got.Port, exp.Port)
				}
				if got.RndcPort != exp.RndcPort {
					t.Errorf("[%d] RndcPort: got %d, want %d", i, got.RndcPort, exp.RndcPort)
				}
				if got.RndcKeyName != exp.RndcKeyName {
					t.Errorf("[%d] RndcKeyName: got %q, want %q", i, got.RndcKeyName, exp.RndcKeyName)
				}
				if got.RndcAlgorithm != exp.RndcAlgorithm {
					t.Errorf("[%d] RndcAlgorithm: got %q, want %q", i, got.RndcAlgorithm, exp.RndcAlgorithm)
				}
				if got.RndcSecret != exp.RndcSecret {
					t.Errorf("[%d] RndcSecret: got %q, want %q", i, got.RndcSecret, exp.RndcSecret)
				}

				if (got.RndcHost == "") != (exp.RndcHost == "") {
					t.Errorf("[%d] RndcHost: got %q, want %q", i, got.RndcHost, exp.RndcHost)
				} else if got.RndcHost != "" && got.RndcHost != exp.RndcHost {
					t.Errorf("[%d] RndcHost: got %q, want %q", i, got.RndcHost, exp.RndcHost)
				}
			}
		})
	}
}
