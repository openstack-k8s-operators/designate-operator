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
	"net/netip"
	"testing"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetNADConfig(t *testing.T) {
	tests := []struct {
		name          string
		nadConfig     string
		expectError   bool
		expectedRange string
		expectedStart string
		expectedEnd   string
	}{
		{
			name: "valid NAD config",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.100"
				}
			}`,
			expectError:   false,
			expectedRange: "192.168.1.0/24",
			expectedStart: "192.168.1.10",
			expectedEnd:   "192.168.1.100",
		},
		{
			name: "valid NAD config with IPv6",
			nadConfig: `{
				"ipam": {
					"range": "2001:db8::/64",
					"range_start": "2001:db8::10",
					"range_end": "2001:db8::100"
				}
			}`,
			expectError:   false,
			expectedRange: "2001:db8::/64",
			expectedStart: "2001:db8::10",
			expectedEnd:   "2001:db8::100",
		},
		{
			name: "invalid JSON",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24"
					"range_start": "192.168.1.10"
				}
			}`,
			expectError: true,
		},
		{
			name: "missing ipam field",
			nadConfig: `{
				"other_field": "value"
			}`,
			expectError:   false,
			expectedRange: "",
			expectedStart: "",
			expectedEnd:   "",
		},
		{
			name: "invalid IP range format",
			nadConfig: `{
				"ipam": {
					"range": "invalid-range",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.100"
				}
			}`,
			expectError: true,
		},
		{
			name: "invalid start IP format",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "invalid-ip",
					"range_end": "192.168.1.100"
				}
			}`,
			expectError: true,
		},
		{
			name: "invalid end IP format",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "invalid-ip"
				}
			}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nad := &networkv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nad",
					Namespace: "test-namespace",
				},
				Spec: networkv1.NetworkAttachmentDefinitionSpec{
					Config: tt.nadConfig,
				},
			}

			config, err := GetNADConfig(nad)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if config == nil {
				t.Errorf("expected config but got nil")
				return
			}

			// Check CIDR
			if tt.expectedRange != "" {
				expectedCIDR, err := netip.ParsePrefix(tt.expectedRange)
				if err != nil {
					t.Fatalf("failed to parse expected CIDR: %v", err)
				}
				if config.IPAM.CIDR != expectedCIDR {
					t.Errorf("expected CIDR %v, got %v", expectedCIDR, config.IPAM.CIDR)
				}
			} else {
				// For empty expected range, check that we got zero value
				var zeroCIDR netip.Prefix
				if config.IPAM.CIDR != zeroCIDR {
					t.Errorf("expected zero CIDR value, got %v", config.IPAM.CIDR)
				}
			}

			// Check start IP
			if tt.expectedStart != "" {
				expectedStart, err := netip.ParseAddr(tt.expectedStart)
				if err != nil {
					t.Fatalf("failed to parse expected start IP: %v", err)
				}
				if config.IPAM.RangeStart != expectedStart {
					t.Errorf("expected start IP %v, got %v", expectedStart, config.IPAM.RangeStart)
				}
			} else {
				// For empty expected start, check that we got zero value
				var zeroAddr netip.Addr
				if config.IPAM.RangeStart != zeroAddr {
					t.Errorf("expected zero start IP value, got %v", config.IPAM.RangeStart)
				}
			}

			// Check end IP
			if tt.expectedEnd != "" {
				expectedEnd, err := netip.ParseAddr(tt.expectedEnd)
				if err != nil {
					t.Fatalf("failed to parse expected end IP: %v", err)
				}
				if config.IPAM.RangeEnd != expectedEnd {
					t.Errorf("expected end IP %v, got %v", expectedEnd, config.IPAM.RangeEnd)
				}
			} else {
				// For empty expected end, check that we got zero value
				var zeroAddr netip.Addr
				if config.IPAM.RangeEnd != zeroAddr {
					t.Errorf("expected zero end IP value, got %v", config.IPAM.RangeEnd)
				}
			}
		})
	}
}

func TestGetNetworkParametersFromNAD(t *testing.T) {
	tests := []struct {
		name                  string
		nadConfig             string
		expectError           bool
		expectedCIDR          string
		expectedProviderStart string
		expectedProviderEnd   string
		errorContains         string
	}{
		{
			name: "valid configuration with sufficient IP space",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.100"
				}
			}`,
			expectError:           false,
			expectedCIDR:          "192.168.1.0/24",
			expectedProviderStart: "192.168.1.101",
			expectedProviderEnd:   "192.168.1.126",
		},
		{
			name: "valid IPv6 configuration",
			nadConfig: `{
				"ipam": {
					"range": "2001:db8::/64",
					"range_start": "2001:db8::10",
					"range_end": "2001:db8::100"
				}
			}`,
			expectError:           false,
			expectedCIDR:          "2001:db8::/64",
			expectedProviderStart: "2001:db8::101",
			expectedProviderEnd:   "2001:db8::11a",
		},
		{
			name: "insufficient IP space - range end too close to network boundary",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.250"
				}
			}`,
			expectError:   true,
			errorContains: "cannot allocate IP addresses: 25 IP addresses in",
		},
		{
			name: "small subnet - /30 network",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/30",
					"range_start": "192.168.1.1",
					"range_end": "192.168.1.2"
				}
			}`,
			expectError:   true,
			errorContains: "cannot allocate IP addresses: 25 IP addresses in",
		},
		{
			name: "range end at network boundary",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.254"
				}
			}`,
			expectError:   true,
			errorContains: "cannot allocate IP addresses: 25 IP addresses in",
		},
		{
			name: "invalid JSON in NAD config",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24"
					"range_start": "192.168.1.10"
				}
			}`,
			expectError:   true,
			errorContains: "cannot read network parameters",
		},
		{
			name: "edge case - exactly enough IP space",
			nadConfig: `{
				"ipam": {
					"range": "192.168.1.0/24",
					"range_start": "192.168.1.10",
					"range_end": "192.168.1.229"
				}
			}`,
			expectError:           false,
			expectedCIDR:          "192.168.1.0/24",
			expectedProviderStart: "192.168.1.230",
			expectedProviderEnd:   "192.168.1.255",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nad := &networkv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nad",
					Namespace: "test-namespace",
				},
				Spec: networkv1.NetworkAttachmentDefinitionSpec{
					Config: tt.nadConfig,
				},
			}

			params, err := GetNetworkParametersFromNAD(nad)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected error but got none")
				}
				if tt.errorContains != "" && err != nil {
					if err.Error() == "" || len(err.Error()) == 0 {
						t.Errorf("expected error message to contain '%s', but error was empty", tt.errorContains)
					} else {
						// Check if error message contains the expected substring
						found := false
						errorMsg := err.Error()
						if len(errorMsg) >= len(tt.errorContains) {
							for i := 0; i <= len(errorMsg)-len(tt.errorContains); i++ {
								if errorMsg[i:i+len(tt.errorContains)] == tt.errorContains {
									found = true
									break
								}
							}
						}
						if !found {
							t.Errorf("expected error message to contain '%s', got '%s'", tt.errorContains, errorMsg)
						}
					}
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if params == nil {
				t.Errorf("expected params but got nil")
				return
			}

			// Check CIDR
			if tt.expectedCIDR != "" {
				expectedCIDR, err := netip.ParsePrefix(tt.expectedCIDR)
				if err != nil {
					t.Fatalf("failed to parse expected CIDR: %v", err)
				}
				if params.CIDR != expectedCIDR {
					t.Errorf("expected CIDR %v, got %v", expectedCIDR, params.CIDR)
				}
			}

			// Check provider allocation start
			if tt.expectedProviderStart != "" {
				expectedStart, err := netip.ParseAddr(tt.expectedProviderStart)
				if err != nil {
					t.Fatalf("failed to parse expected provider start IP: %v", err)
				}
				if params.ProviderAllocationStart != expectedStart {
					t.Errorf("expected provider start IP %v, got %v", expectedStart, params.ProviderAllocationStart)
				}
			}

			// Check provider allocation end
			if tt.expectedProviderEnd != "" {
				expectedEnd, err := netip.ParseAddr(tt.expectedProviderEnd)
				if err != nil {
					t.Fatalf("failed to parse expected provider end IP: %v", err)
				}
				if params.ProviderAllocationEnd != expectedEnd {
					t.Errorf("expected provider end IP %v, got %v", expectedEnd, params.ProviderAllocationEnd)
				}
			}

			// Verify that all provider allocation IPs are within the CIDR
			if !tt.expectError && tt.expectedCIDR != "" {
				if !params.CIDR.Contains(params.ProviderAllocationStart) {
					t.Errorf("provider allocation start %v is not within CIDR %v", params.ProviderAllocationStart, params.CIDR)
				}
				if !params.CIDR.Contains(params.ProviderAllocationEnd) {
					t.Errorf("provider allocation end %v is not within CIDR %v", params.ProviderAllocationEnd, params.CIDR)
				}
			}
		})
	}
}

func TestGetNetworkParametersFromNAD_ProviderAllocationSize(t *testing.T) {
	// Test that the provider allocation pool has exactly BindProvPredictablePoolSize (25) IP addresses
	nadConfig := `{
		"ipam": {
			"range": "192.168.1.0/24",
			"range_start": "192.168.1.10",
			"range_end": "192.168.1.100"
		}
	}`

	nad := &networkv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nad",
			Namespace: "test-namespace",
		},
		Spec: networkv1.NetworkAttachmentDefinitionSpec{
			Config: nadConfig,
		},
	}

	params, err := GetNetworkParametersFromNAD(nad)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count the number of IP addresses in the provider allocation range
	// The range is from start (inclusive) to end (exclusive)
	start := params.ProviderAllocationStart
	end := params.ProviderAllocationEnd
	count := 0
	current := start

	for current != end {
		count++
		current = current.Next()

		// Safety check to prevent infinite loop
		if count > 30 {
			t.Fatalf("counted more than 30 IPs, something is wrong")
		}
	}

	if count != BindProvPredictablePoolSize {
		t.Errorf("expected provider allocation pool size %d, got %d", BindProvPredictablePoolSize, count)
	}
}

func TestNetworkParameters_StructFields(t *testing.T) {
	// Test that NetworkParameters struct fields are properly set
	cidr, _ := netip.ParsePrefix("192.168.1.0/24")
	start, _ := netip.ParseAddr("192.168.1.101")
	end, _ := netip.ParseAddr("192.168.1.125")

	params := &NetworkParameters{
		CIDR:                    cidr,
		ProviderAllocationStart: start,
		ProviderAllocationEnd:   end,
	}

	if params.CIDR != cidr {
		t.Errorf("expected CIDR %v, got %v", cidr, params.CIDR)
	}
	if params.ProviderAllocationStart != start {
		t.Errorf("expected start %v, got %v", start, params.ProviderAllocationStart)
	}
	if params.ProviderAllocationEnd != end {
		t.Errorf("expected end %v, got %v", end, params.ProviderAllocationEnd)
	}
}

func TestNADConfig_StructFields(t *testing.T) {
	// Test that NADConfig and NADIpam structs are properly structured
	cidr, _ := netip.ParsePrefix("192.168.1.0/24")
	start, _ := netip.ParseAddr("192.168.1.10")
	end, _ := netip.ParseAddr("192.168.1.100")

	ipam := NADIpam{
		CIDR:       cidr,
		RangeStart: start,
		RangeEnd:   end,
	}

	config := &NADConfig{
		IPAM: ipam,
	}

	if config.IPAM.CIDR != cidr {
		t.Errorf("expected CIDR %v, got %v", cidr, config.IPAM.CIDR)
	}
	if config.IPAM.RangeStart != start {
		t.Errorf("expected start %v, got %v", start, config.IPAM.RangeStart)
	}
	if config.IPAM.RangeEnd != end {
		t.Errorf("expected end %v, got %v", end, config.IPAM.RangeEnd)
	}
}
