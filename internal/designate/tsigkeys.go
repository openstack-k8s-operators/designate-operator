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
	"regexp"

	"github.com/gophercloud/gophercloud/v2"

	"github.com/openstack-k8s-operators/lib-common/modules/openstack"
)

var validTSIGKeyName = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// IsValidTSIGKeyName checks that a TSIG key name is safe for injection into BIND config files.
func IsValidTSIGKeyName(name string) bool {
	return len(name) > 0 && validTSIGKeyName.MatchString(name)
}

// TODO: Replace this custom implementation with upstream Gophercloud support when available.
// This package implements TSIG key operations that are currently missing from Gophercloud.
// Once https://github.com/gophercloud/gophercloud adds DNS v2 TSIG key support, migrate to:
// import "github.com/gophercloud/gophercloud/v2/openstack/dns/v2/tsigkeys"

// TSIGKey represents a TSIG (Transaction Signature) key for DNS authentication
type TSIGKey struct {
	ID         string `json:"id"`
	Scope      string `json:"scope"`
	ResourceID string `json:"resource_id"`
	Name       string `json:"name"`
	Algorithm  string `json:"algorithm"` // e.g., "hmac-sha256"
	Secret     string `json:"secret"`    // Base64-encoded key
}

// ListAllTSIGKeys retrieves all TSIG keys with full details including ID
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

	allKeys, err := ListAllTSIGKeys(ctx, osclient)
	if err != nil {
		return fmt.Errorf("failed to list TSIG keys: %w", err)
	}

	var keyID string
	for _, key := range allKeys {
		if key.Name == name {
			keyID = key.ID
			break
		}
	}

	if keyID == "" {
		return nil
	}

	url := dnsClient.ServiceURL("tsigkeys", keyID)
	_, err = dnsClient.Delete(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("failed to delete TSIG key %s (ID: %s): %w", name, keyID, err)
	}

	return nil
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

	return createTSIGKey(ctx, dnsClient, opts)
}

func createTSIGKey(ctx context.Context, dnsClient *gophercloud.ServiceClient, opts CreateTSIGKeyOpts) (*TSIGKey, error) {
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

	_, err := dnsClient.Post(ctx, url, reqBody, &result, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create TSIG key: %w", err)
	}

	id, _ := result["id"].(string)
	name, _ := result["name"].(string)
	algorithm, _ := result["algorithm"].(string)
	secret, _ := result["secret"].(string)

	tsigKey := &TSIGKey{
		ID:         id,
		Name:       name,
		Algorithm:  algorithm,
		Secret:     secret,
		Scope:      opts.Scope,
		ResourceID: opts.ResourceID,
	}

	return tsigKey, nil
}

// EnsureTSIGKeyPool repairs a stale pool binding without replacing key material.
// Retaining the key ID, name and secret keeps running BIND pods authenticated.
func EnsureTSIGKeyPool(ctx context.Context, osclient *openstack.OpenStack, key *TSIGKey, poolID string) error {
	if key.Scope == "POOL" && key.ResourceID == poolID {
		return nil
	}
	dnsClient, err := GetDNSClient(osclient)
	if err != nil {
		return err
	}
	return ensureTSIGKeyPool(ctx, dnsClient, key, poolID)
}

func ensureTSIGKeyPool(ctx context.Context, dnsClient *gophercloud.ServiceClient, key *TSIGKey, poolID string) error {
	if key.Scope == "POOL" && key.ResourceID == poolID {
		return nil
	}
	body := map[string]string{"scope": "POOL", "resource_id": poolID}
	_, err := dnsClient.Patch(ctx, dnsClient.ServiceURL("tsigkeys", key.ID), body, nil, &gophercloud.RequestOpts{OkCodes: []int{200}})
	if err != nil {
		return fmt.Errorf("failed to bind TSIG key %s to pool %s: %w", key.Name, poolID, err)
	}
	key.Scope = "POOL"
	key.ResourceID = poolID
	return nil
}
