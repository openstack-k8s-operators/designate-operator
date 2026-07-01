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
	"fmt"

	"github.com/gophercloud/gophercloud/v2/openstack/dns/v2/tsigkeys"
	"github.com/openstack-k8s-operators/lib-common/modules/openstack"
)

// GetTSIGKeyByName retrieves a TSIG key by name.
// Returns nil if not found.
func GetTSIGKeyByName(
	ctx context.Context,
	osclient *openstack.OpenStack,
	name string,
) (*tsigkeys.TSIGKey, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	allPages, err := tsigkeys.List(dnsClient, tsigkeys.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	keys, err := tsigkeys.ExtractTSIGKeys(allPages)
	if err != nil {
		return nil, fmt.Errorf("failed to extract TSIG keys: %w", err)
	}

	if len(keys) == 0 {
		return nil, nil
	}

	return &keys[0], nil
}

// CreateTSIGKey creates a new TSIG key in Designate.
func CreateTSIGKey(
	ctx context.Context,
	osclient *openstack.OpenStack,
	opts tsigkeys.CreateOpts,
) (*tsigkeys.TSIGKey, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	key, err := tsigkeys.Create(ctx, dnsClient, opts).Extract()
	if err != nil {
		return nil, fmt.Errorf("failed to create TSIG key: %w", err)
	}

	return key, nil
}

// DeleteTSIGKeyByName deletes a TSIG key by name from Designate.
// Returns nil if the key does not exist.
func DeleteTSIGKeyByName(
	ctx context.Context,
	osclient *openstack.OpenStack,
	name string,
) error {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return fmt.Errorf("failed to get DNS client: %w", err)
	}

	allPages, err := tsigkeys.List(dnsClient, tsigkeys.ListOpts{Name: name}).AllPages(ctx)
	if err != nil {
		return fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	keys, err := tsigkeys.ExtractTSIGKeys(allPages)
	if err != nil {
		return fmt.Errorf("failed to extract TSIG keys: %w", err)
	}

	if len(keys) == 0 {
		return nil
	}

	err = tsigkeys.Delete(ctx, dnsClient, keys[0].ID).ExtractErr()
	if err != nil {
		return fmt.Errorf("failed to delete TSIG key %s (ID: %s): %w", name, keys[0].ID, err)
	}

	return nil
}
