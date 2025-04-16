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
	IPAM NADIpam `json:"ipam"`
}

// NADIpam represents network attachment definition IPAM configuration
type NADIpam struct {
	CIDR       netip.Prefix `json:"range"`
	RangeStart netip.Addr   `json:"range_start"`
	RangeEnd   netip.Addr   `json:"range_end"`
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
	for i := 0; i < BindProvPredictablePoolSize; i++ {
		if !networkParameters.CIDR.Contains(end) {
			return nil, fmt.Errorf("%w: %d IP addresses in %s", ErrCannotAllocateIPAddresses, BindProvPredictablePoolSize, networkParameters.CIDR)
		}
		end = end.Next()
	}
	networkParameters.ProviderAllocationEnd = end

	return networkParameters, err
}
