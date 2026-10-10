/*
Licensed under the Apache License, Version 2.0 (the "License");
@you may not use this file except in compliance with the License.
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
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
)

// NetworkParameters - Parameters for the Designate networks, based on the config of the NAD
type NetworkParameters struct {
	CIDR                    netip.Prefix
	ProviderAllocationStart netip.Addr
	ProviderAllocationEnd   netip.Addr
}

// NADConfig - IPAM parameters of the NAD
type NADConfig struct {
	Name           string       `json:"name"`
	NADType        string       `json:"type"`
	Topology       string       `json:"topology"`
	IPAM           NADIpam      `json:"ipam"`
	Subnets        netip.Prefix `json:"subnets"`
	ExcludeSubnets string       `json:"excludeSubnets"`
}

// NADIpam represents network attachment definition IPAM configuration
type NADIpam struct {
	CIDR       netip.Prefix `json:"range"`
	RangeStart netip.Addr   `json:"range_start"`
	RangeEnd   netip.Addr   `json:"range_end"`
}

// GetRangeFromCIDR - compute a IP address range from a CIDR
func GetRangeFromCIDR(
	cidr netip.Prefix,
) (start netip.Addr, end netip.Addr) {
	// start is the 5th address of the Cidr
	start = cidr.Masked().Addr()
	for range 5 {
		start = start.Next()
	}

	bits := cidr.Bits()
	if start.Is4() {
		// Padding for ipv4 addresses in a [16]bytes table
		bits += 96
	}
	// convert it to a [16]bytes table, set the remaining bits to 1
	addrBytes := start.As16()
	for b := bits; b < 128; b++ {
		addrBytes[b/8] |= 1 << uint(7-(b%8)) // #nosec G115,G602 -- Controlled bit manipulation with small integer values, b < 128 guarantees b/8 < 16
	}
	// convert the table to an ip address to get the last IP
	// in case of IPv4, the address should be unmapped
	last := netip.AddrFrom16(addrBytes)
	if start.Is4() {
		last = last.Unmap()
	}
	// end is the 2nd last
	end = last.Prev()

	return
}

// GetNADConfig parses and returns the NAD configuration from a NetworkAttachmentDefinition
func GetNADConfig(
	nad *networkv1.NetworkAttachmentDefinition,
) (*NADConfig, error) {
	nadConfig := &NADConfig{}
	jsonDoc := []byte(nad.Spec.Config)
	err := json.Unmarshal(jsonDoc, nadConfig)
	if err != nil {
		return nil, err
	}
	return nadConfig, nil
}

// GetNetworkParametersFromNAD - Extract network information from the Network Attachment Definition
func GetNetworkParametersFromNAD(
	nad *networkv1.NetworkAttachmentDefinition,
) (*NetworkParameters, error) {
	networkParameters := &NetworkParameters{}

	nadConfig, err := GetNADConfig(nad)
	if err != nil {
		return nil, fmt.Errorf("cannot read network parameters: %w", err)
	}

	if nadConfig.NADType == "ovn-k8s-cni-overlay" {
		// If overlay, the range is in the excludeSubnets field.
		networkParameters.CIDR = nadConfig.Subnets

		if len(nadConfig.ExcludeSubnets) == 0 {
			return nil, fmt.Errorf("network attachment definition %s must exclude a range for predictable ips", nadConfig.Name)
		}
		excludedSubnets := strings.Split(nadConfig.ExcludeSubnets, ",")

		if len(strings.TrimSpace(excludedSubnets[0])) == 0 {
			return nil, fmt.Errorf("network attachment definition %s must exclude a range for predictable ips", nadConfig.Name)
		}

		prefix, err := netip.ParsePrefix(excludedSubnets[0])
		if err != err {
			return nil, err
		}
		networkParameters.ProviderAllocationStart, networkParameters.ProviderAllocationEnd =
			GetRangeFromCIDR(prefix)
		return networkParameters, nil
	}

	// Designate CIDR parameters
	// These are the parameters for Designate's net/subnet
	networkParameters.CIDR = nadConfig.IPAM.CIDR

	// OpenShift allocates IP addresses from IPAM.RangeStart to IPAM.RangeEnd
	// for the pods.
	// We're going to use a range of 25 IP addresses that are assigned to
	// the Neutron allocation pool, the range starts right after OpenShift
	// RangeEnd.
	networkParameters.ProviderAllocationStart = nadConfig.IPAM.RangeEnd.Next()
	end := networkParameters.ProviderAllocationStart
	for range BindProvPredictablePoolSize {
		if !networkParameters.CIDR.Contains(end) {
			return nil, fmt.Errorf("%w: %d IP addresses in %s", ErrCannotAllocateIPAddresses, BindProvPredictablePoolSize, networkParameters.CIDR)
		}
		end = end.Next()
	}
	networkParameters.ProviderAllocationEnd = end

	return networkParameters, err
}
