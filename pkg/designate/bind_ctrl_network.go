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
	"fmt"
)

// GetPredictableIPAM returns a struct describing the available IP range. If the
// IP pool size does not fit in given networkParameters CIDR it will return an
// error instead.
func GetPredictableIPAM(networkParameters *NetworkParameters) (*NADIpam, error) {
	predParams := &NADIpam{}
	predParams.CIDR = networkParameters.CIDR
	predParams.RangeStart = networkParameters.ProviderAllocationEnd.Next()
	endRange := predParams.RangeStart
	for i := 0; i < BindProvPredictablePoolSize; i++ {
		if !predParams.CIDR.Contains(endRange) {
			return nil, fmt.Errorf("predictable IPs: cannot allocate %d IP addresses in %s", BindProvPredictablePoolSize, predParams.CIDR)
		}
		endRange = endRange.Next()
	}
	predParams.RangeEnd = endRange
	return predParams, nil
}

func GetNextIP(predParams *NADIpam, allocatedIPs map[string]bool) (string, error) {
	for candidateAddress := predParams.RangeStart; candidateAddress != predParams.RangeEnd; candidateAddress = candidateAddress.Next() {
		if !allocatedIPs[candidateAddress.String()] {
			allocatedIPs[candidateAddress.String()] = true
			return candidateAddress.String(), nil
		}
	}
	return "", fmt.Errorf("predictable IPs: out of available addresses")
}
