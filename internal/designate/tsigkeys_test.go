/*
Copyright 2026 Red Hat

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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gophercloud/gophercloud/v2"
)

func TestTSIGPoolBindingMigration(t *testing.T) {
	const poolID = "11111111-1111-1111-1111-111111111111"
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "repair", true: "API failure preserves old binding"}[fail], func(t *testing.T) {
			key := &TSIGKey{ID: "existing-key", Name: "pool1-tsig", Secret: "unchanged", Algorithm: "hmac-sha256", Scope: "POOL", ResourceID: "00000000-0000-0000-0000-000000000000"} // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodPatch || r.URL.Path != "/v2/tsigkeys/existing-key" {
					t.Errorf("unexpected migration request: %s %s", r.Method, r.URL.Path)
				}
				var body map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if len(body) != 2 || body["scope"] != "POOL" || body["resource_id"] != poolID {
					t.Errorf("migration must only update pool binding: %v", body)
				}
				w.Header().Set("Content-Type", "application/json")
				if fail {
					w.WriteHeader(http.StatusBadRequest)
				} else {
					w.WriteHeader(http.StatusOK)
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			client := &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: server.URL + "/v2/"}
			err := ensureTSIGKeyPool(context.Background(), client, key, poolID)
			if (err != nil) != fail {
				t.Fatalf("unexpected migration result: %v", err)
			}
			if key.ID != "existing-key" || key.Name != "pool1-tsig" || key.Secret != "unchanged" {
				t.Fatal("migration rotated existing key material")
			}
			if fail {
				if key.ResourceID == poolID {
					t.Fatal("failed update published an unregistered binding")
				}
			} else {
				if key.Scope != "POOL" || key.ResourceID != poolID {
					t.Fatalf("incorrect pool binding: %+v", key)
				}
				if err := ensureTSIGKeyPool(context.Background(), client, key, poolID); err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("valid per-pool key should be reused, got %d writes", calls)
				}
			}
		})
	}
}

func TestCreateTSIGKeyRegistersRealPool(t *testing.T) {
	const poolID = "11111111-1111-1111-1111-111111111111"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v2/tsigkeys" {
			t.Errorf("unexpected key creation: %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body["scope"] != "POOL" || body["resource_id"] != poolID {
			t.Errorf("key is not registered to a real pool: %v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		body["id"] = "real-key-uuid"
		_ = json.NewEncoder(w).Encode(body)
	}))
	defer server.Close()
	client := &gophercloud.ServiceClient{ProviderClient: &gophercloud.ProviderClient{}, Endpoint: server.URL + "/v2/"}
	key, err := createTSIGKey(context.Background(), client, CreateTSIGKeyOpts{Name: "pool1-tsig", Algorithm: "hmac-sha256", Secret: "c2VjcmV0", Scope: "POOL", ResourceID: poolID}) // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != "real-key-uuid" || key.Scope != "POOL" || key.ResourceID != poolID {
		t.Fatalf("key lost its real identity: %+v", key)
	}
}
