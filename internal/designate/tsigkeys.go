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

	"github.com/openstack-k8s-operators/lib-common/modules/openstack"
)

// TODO: Replace this custom implementation with upstream Gophercloud support when available.
// This package implements TSIG key operations that are currently missing from Gophercloud.
// Once https://github.com/gophercloud/gophercloud adds DNS v2 TSIG key support, migrate to:
// import "github.com/gophercloud/gophercloud/v2/openstack/dns/v2/tsigkeys"

// TSIGKey represents a TSIG (Transaction Signature) key for DNS authentication
type TSIGKey struct {
	Name      string `json:"name"`
	Algorithm string `json:"algorithm"` // e.g., "hmac-sha256"
	Secret    string `json:"secret"`    // Base64-encoded key
}

// ListAllTSIGKeys retrieves all TSIG keys (no filtering)
func ListAllTSIGKeys(
	ctx context.Context,
	osclient *openstack.OpenStack,
) ([]TSIGKey, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	url := dnsClient.ServiceURL("tsigkeys")

	var result struct {
		TSIGKeys []TSIGKey `json:"tsigkeys"`
	}

	_, err = dnsClient.Get(ctx, url, &result, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	return result.TSIGKeys, nil
}

// GetTSIGKeyByName retrieves a TSIG key by name
func GetTSIGKeyByName(
	ctx context.Context,
	osclient *openstack.OpenStack,
	name string,
) (*TSIGKey, error) {
	keys, err := ListAllTSIGKeys(ctx, osclient)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		if key.Name == name {
			return &key, nil
		}
	}

	return nil, nil // Not found
}

// DeleteTSIGKeyByName deletes a TSIG key by name from Designate
func DeleteTSIGKeyByName(
	ctx context.Context,
	osclient *openstack.OpenStack,
	name string,
) error {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return fmt.Errorf("failed to get DNS client: %w", err)
	}

	// Get all TSIG keys with full details including ID
	allKeys, err := ListAllTSIGKeysWithID(ctx, osclient)
	if err != nil {
		return fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	// Find the key with matching name
	var keyID string
	for _, key := range allKeys {
		if keyName, ok := key["name"].(string); ok && keyName == name {
			if id, ok := key["id"].(string); ok {
				keyID = id
				break
			}
		}
	}

	if keyID == "" {
		// Key not found, nothing to delete
		return nil
	}

	// Delete the TSIG key
	url := dnsClient.ServiceURL("tsigkeys", keyID)
	_, err = dnsClient.Delete(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("failed to delete TSIG key %s (ID: %s): %w", name, keyID, err)
	}

	return nil
}

// ListAllTSIGKeysWithID retrieves all TSIG keys with full details including ID
func ListAllTSIGKeysWithID(
	ctx context.Context,
	osclient *openstack.OpenStack,
) ([]map[string]interface{}, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	url := dnsClient.ServiceURL("tsigkeys")

	var result struct {
		TSIGKeys []map[string]interface{} `json:"tsigkeys"`
	}

	_, err = dnsClient.Get(ctx, url, &result, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	return result.TSIGKeys, nil
}

// CreateTSIGKeyOpts represents options for creating a TSIG key
type CreateTSIGKeyOpts struct {
	Name       string `json:"name" required:"true"`
	Algorithm  string `json:"algorithm" required:"true"`
	Secret     string `json:"secret,omitempty"`
	Scope      string `json:"scope" required:"true"`
	ResourceID string `json:"resource_id" required:"true"`
}

// CreateTSIGKey creates a new TSIG key in Designate
func CreateTSIGKey(
	ctx context.Context,
	osclient *openstack.OpenStack,
	opts CreateTSIGKeyOpts,
) (*TSIGKey, error) {
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS client: %w", err)
	}

	url := dnsClient.ServiceURL("tsigkeys")

	reqBody := map[string]interface{}{
		"name":        opts.Name,
		"algorithm":   opts.Algorithm,
		"scope":       opts.Scope,
		"resource_id": opts.ResourceID,
	}

	// Only include secret if provided, otherwise Designate will generate one
	if opts.Secret != "" {
		reqBody["secret"] = opts.Secret
	}

	var result map[string]interface{}

	_, err = dnsClient.Post(ctx, url, reqBody, &result, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create TSIG key: %w", err)
	}

	// Extract TSIGKey fields from response
	tsigKey := &TSIGKey{
		Name:      result["name"].(string),
		Algorithm: result["algorithm"].(string),
		Secret:    result["secret"].(string),
	}

	return tsigKey, nil
}
