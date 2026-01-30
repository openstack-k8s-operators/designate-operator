/*
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
	"context"
	"errors"
	"fmt"

	"github.com/gophercloud/gophercloud/v2"
	"github.com/gophercloud/gophercloud/v2/openstack/dns/v2/zones"
	"github.com/openstack-k8s-operators/lib-common/modules/openstack"
)

var (
	// ErrZoneNotFound is returned when a zone is not found
	ErrZoneNotFound = errors.New("zone not found")
)

// ListZonesInPool queries the Designate API for zones in a specific pool
// Returns the list of zones and any error encountered
//
// Note: Gophercloud's zones.ListOpts doesn't support pool_id filtering, so we fetch
// all zones and filter client-side. This is acceptable since pool removal is an
// infrequent operation.
func ListZonesInPool(
	ctx context.Context,
	osclient *openstack.OpenStack,
	poolID string,
) ([]zones.Zone, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	// List all zones (Gophercloud doesn't support pool_id filter)
	listOpts := zones.ListOpts{}

	allPages, err := zones.List(dnsClient, listOpts).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list zones: %w", err)
	}

	allZones, err := zones.ExtractZones(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract zones from response: %w", err)
	}

	// Filter zones by pool_id client-side
	var poolZones []zones.Zone
	for _, zone := range allZones {
		if zone.PoolID == poolID {
			poolZones = append(poolZones, zone)
		}
	}

	return poolZones, nil
}

// HasZonesInPool checks if a pool contains any DNS zones
// Returns true if zones exist, false otherwise
func HasZonesInPool(
	ctx context.Context,
	osclient *openstack.OpenStack,
	poolID string,
) (bool, error) {
	zoneList, err := ListZonesInPool(ctx, osclient, poolID)
	if err != nil {
		return false, err
	}

	return len(zoneList) > 0, nil
}

// CountZonesInPool returns the number of zones in a specific pool
func CountZonesInPool(
	ctx context.Context,
	osclient *openstack.OpenStack,
	poolID string,
) (int, error) {
	zoneList, err := ListZonesInPool(ctx, osclient, poolID)
	if err != nil {
		return 0, err
	}

	return len(zoneList), nil
}

// GetZone retrieves a specific zone by ID
func GetZone(
	ctx context.Context,
	osclient *openstack.OpenStack,
	zoneID string,
) (*zones.Zone, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	zone, err := zones.Get(ctx, dnsClient, zoneID).Extract()
	if err != nil {
		// Check if it's a 404 error
		if gophercloud.ResponseCodeIs(err, 404) {
			return nil, fmt.Errorf("%w: %s", ErrZoneNotFound, zoneID)
		}
		return nil, fmt.Errorf("failed to get zone %s: %w", zoneID, err)
	}

	return zone, nil
}
