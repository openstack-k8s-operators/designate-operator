/*
Copyright 2026.

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

package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	fakekclient "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
)

// These tests cover the Kubernetes Secret side of TSIG key management for
// multipool DesignateBackendbind9 (creation, content, annotations and
// cleanup) using a fake client. They intentionally do not exercise the
// Designate DNS API calls in ensurePerPoolTSIGKeys/ensureSharedTSIGKey
// (designate.CreateTSIGKey et al.) since those require a live/mocked
// OpenStack service catalog that doesn't exist in this test suite yet.

const testBackendbind9Name = "backendbind9"
const testBackendbind9Namespace = "test-namespace"

func newTSIGTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add client-go scheme: %v", err)
	}
	if err := designatev1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("failed to add designate scheme: %v", err)
	}
	return scheme
}

// newTSIGTestFixtures builds a DesignateBackendbind9Reconciler, its helper and
// the owning instance, backed by a fake client seeded with extraObjs.
func newTSIGTestFixtures(t *testing.T, extraObjs ...client.Object) (*DesignateBackendbind9Reconciler, *helper.Helper, *designatev1beta1.DesignateBackendbind9) {
	t.Helper()
	scheme := newTSIGTestScheme(t)

	instance := &designatev1beta1.DesignateBackendbind9{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testBackendbind9Name,
			Namespace: testBackendbind9Namespace,
			UID:       "backendbind9-test-uid",
		},
	}

	objs := append([]client.Object{instance}, extraObjs...)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

	r := &DesignateBackendbind9Reconciler{
		Client:  fakeClient,
		Kclient: fakekclient.NewSimpleClientset(),
		Scheme:  scheme,
	}

	h, err := helper.NewHelper(instance, r.Client, r.Kclient, r.Scheme, logr.Discard())
	if err != nil {
		t.Fatalf("failed to create helper: %v", err)
	}

	return r, h, instance
}

// secretStringValue returns a data value from a Secret regardless of whether
// the fake client persisted it under StringData or Data.
func secretStringValue(secret *corev1.Secret, key string) (string, bool) {
	if v, ok := secret.StringData[key]; ok {
		return v, true
	}
	if v, ok := secret.Data[key]; ok {
		return string(v), true
	}
	return "", false
}

func Test_tsigSecretNameForPool(t *testing.T) {
	tests := []struct {
		name         string
		instanceName string
		poolIdx      int
		want         string
	}{
		{"default pool keeps base name", "backendbind9", 0, "backendbind9" + designate.TsigSecretSuffix},
		{"pool 1 gets pool-indexed name", "backendbind9", 1, "backendbind9-pool1" + designate.TsigSecretSuffix},
		{"pool 2 gets pool-indexed name", "backendbind9", 2, "backendbind9-pool2" + designate.TsigSecretSuffix},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tsigSecretNameForPool(tt.instanceName, tt.poolIdx); got != tt.want {
				t.Errorf("tsigSecretNameForPool(%q, %d) = %q, want %q", tt.instanceName, tt.poolIdx, got, tt.want)
			}
		})
	}
}

func Test_generateTSIGConfig(t *testing.T) {
	r := &DesignateBackendbind9Reconciler{}

	t.Run("valid key with mdns IPs produces the complete rendered config", func(t *testing.T) {
		key := &designate.TSIGKey{Name: "default-tsig-key", Algorithm: "hmac-sha256", Secret: "c2VjcmV0"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
		got := r.generateTSIGConfig(key, []string{"10.0.0.1", "10.0.0.2"})

		want := `key "default-tsig-key" {
    algorithm hmac-sha256;
    secret "c2VjcmV0";
};

server 10.0.0.1 {
    keys { default-tsig-key; };
};
server 10.0.0.2 {
    keys { default-tsig-key; };
};
`
		if got != want {
			t.Errorf("generateTSIGConfig() = %q, want %q", got, want)
		}
	})

	t.Run("no mdns IPs yields key block only", func(t *testing.T) {
		key := &designate.TSIGKey{Name: "default-tsig-key", Algorithm: "hmac-sha256", Secret: "c2VjcmV0"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
		got := r.generateTSIGConfig(key, nil)
		if !containsSubstring(got, `key "default-tsig-key" {`) {
			t.Errorf("generateTSIGConfig() missing key block, got: %q", got)
		}
		if containsSubstring(got, "server ") {
			t.Errorf("generateTSIGConfig() should not contain server blocks, got: %q", got)
		}
	})

	t.Run("invalid key name is rejected", func(t *testing.T) {
		key := &designate.TSIGKey{Name: "bad key; rm -rf", Algorithm: "hmac-sha256", Secret: "c2VjcmV0"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
		got := r.generateTSIGConfig(key, []string{"10.0.0.1"})
		if got != "" {
			t.Errorf("generateTSIGConfig() with invalid key name = %q, want empty string", got)
		}
	})
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (func() bool {
		for i := 0; i+len(substr) <= len(s); i++ {
			if s[i:i+len(substr)] == substr {
				return true
			}
		}
		return false
	})()
}

// Test_createOrUpdatePerPoolTSIGSecret_DefaultPool verifies that the default
// pool (pool index 0) gets its own TSIG Secret carrying both its own
// tsigkeys.conf and the canonical pool-name -> tsigkey-id JSON mapping.
func Test_createOrUpdatePerPoolTSIGSecret_DefaultPool(t *testing.T) {
	ctx := context.Background()
	r, h, instance := newTSIGTestFixtures(t)

	defaultKey := &designate.TSIGKey{ID: "default-key-id", Name: "default-tsig-key", Algorithm: "hmac-sha256", Secret: "c2VjcmV0"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
	pool1Key := &designate.TSIGKey{ID: "pool1-key-id", Name: "pool1-tsig-key", Algorithm: "hmac-sha256", Secret: "c2VjcmV0Mg=="}   // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
	allKeys := map[string]*designate.TSIGKey{
		"default": defaultKey,
		"pool1":   pool1Key,
	}

	secretName := tsigSecretNameForPool(instance.Name, 0)
	tsigConfigContent := "key \"default-tsig-key\" {\n};\n"

	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, secretName, defaultKey, tsigConfigContent, "hash-1", allKeys); err != nil {
		t.Fatalf("createOrUpdatePerPoolTSIGSecret() error = %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: instance.Namespace}, secret); err != nil {
		t.Fatalf("failed to get TSIG secret: %v", err)
	}

	if secretName != instance.Name+designate.TsigSecretSuffix {
		t.Fatalf("expected default pool secret to keep base name, got %q", secretName)
	}

	if got, ok := secretStringValue(secret, "tsigkeys.conf"); !ok || got != tsigConfigContent {
		t.Errorf("tsigkeys.conf = %q, ok=%v, want %q", got, ok, tsigConfigContent)
	}

	if secret.Annotations["pool-config-hash"] != "hash-1" {
		t.Errorf("pool-config-hash annotation = %q, want %q", secret.Annotations["pool-config-hash"], "hash-1")
	}
	if secret.Annotations["tsig-mode"] != "per-pool" {
		t.Errorf("tsig-mode annotation = %q, want %q", secret.Annotations["tsig-mode"], "per-pool")
	}
	if secret.Annotations["tsigkey-id"] != defaultKey.ID {
		t.Errorf("tsigkey-id annotation = %q, want %q", secret.Annotations["tsigkey-id"], defaultKey.ID)
	}

	// The canonical (pool0) secret must carry the full pool-name -> tsigkey-id mapping.
	mappingJSON, ok := secretStringValue(secret, designate.TSIGKeyIDsDataKey)
	if !ok {
		t.Fatalf("expected canonical secret to carry %s", designate.TSIGKeyIDsDataKey)
	}
	var mapping map[string]string
	if err := json.Unmarshal([]byte(mappingJSON), &mapping); err != nil {
		t.Fatalf("failed to unmarshal tsigkey-ids mapping: %v", err)
	}
	if mapping["default"] != defaultKey.ID || mapping["pool1"] != pool1Key.ID {
		t.Errorf("tsigkey-ids mapping = %+v, want default=%s pool1=%s", mapping, defaultKey.ID, pool1Key.ID)
	}

	foundOwner := false
	for _, ref := range secret.OwnerReferences {
		if ref.Name == instance.Name && ref.Controller != nil && *ref.Controller {
			foundOwner = true
		}
	}
	if !foundOwner {
		t.Errorf("expected secret to have a controller owner reference to %s, got %+v", instance.Name, secret.OwnerReferences)
	}

	if secret.Labels["service"] != "designate-backendbind9" || secret.Labels["component"] != "designate-backendbind9" {
		t.Errorf("unexpected labels: %+v", secret.Labels)
	}
}

// Test_createOrUpdatePerPoolTSIGSecret_NonDefaultPool verifies a non-default
// pool's Secret only carries its own key data, not the tsigkey-id mapping.
func Test_createOrUpdatePerPoolTSIGSecret_NonDefaultPool(t *testing.T) {
	ctx := context.Background()
	r, h, instance := newTSIGTestFixtures(t)

	pool1Key := &designate.TSIGKey{ID: "pool1-key-id", Name: "pool1-tsig-key", Algorithm: "hmac-sha256", Secret: "c2VjcmV0Mg=="} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
	allKeys := map[string]*designate.TSIGKey{"pool1": pool1Key}

	secretName := tsigSecretNameForPool(instance.Name, 1)
	tsigConfigContent := "key \"pool1-tsig-key\" {\n};\n"

	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, secretName, pool1Key, tsigConfigContent, "hash-1", allKeys); err != nil {
		t.Fatalf("createOrUpdatePerPoolTSIGSecret() error = %v", err)
	}

	if secretName != instance.Name+"-pool1"+designate.TsigSecretSuffix {
		t.Fatalf("expected pool-indexed secret name, got %q", secretName)
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: instance.Namespace}, secret); err != nil {
		t.Fatalf("failed to get TSIG secret: %v", err)
	}

	if got, ok := secretStringValue(secret, "tsigkeys.conf"); !ok || got != tsigConfigContent {
		t.Errorf("tsigkeys.conf = %q, ok=%v, want %q", got, ok, tsigConfigContent)
	}
	if _, ok := secretStringValue(secret, designate.TSIGKeyIDsDataKey); ok {
		t.Errorf("non-canonical pool secret should not carry %s", designate.TSIGKeyIDsDataKey)
	}
	if secret.Annotations["tsigkey-id"] != pool1Key.ID {
		t.Errorf("tsigkey-id annotation = %q, want %q", secret.Annotations["tsigkey-id"], pool1Key.ID)
	}
}

// Test_createOrUpdatePerPoolTSIGSecret_Update verifies key rotation: calling
// the reconcile helper again with new key material updates the existing
// Secret in place rather than leaving stale data behind.
func Test_createOrUpdatePerPoolTSIGSecret_Update(t *testing.T) {
	ctx := context.Background()
	r, h, instance := newTSIGTestFixtures(t)

	secretName := tsigSecretNameForPool(instance.Name, 0)
	oldKey := &designate.TSIGKey{ID: "old-id", Name: "default-tsig-key", Algorithm: "hmac-sha256", Secret: "b2xk"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential
	newKey := &designate.TSIGKey{ID: "new-id", Name: "default-tsig-key", Algorithm: "hmac-sha256", Secret: "bmV3"} // #nosec G101 -- fake TSIG secret for test purposes only, not a real credential

	oldKeys := map[string]*designate.TSIGKey{"default": oldKey}
	newKeys := map[string]*designate.TSIGKey{"default": newKey}

	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, secretName, oldKey, "old-config", "hash-1", oldKeys); err != nil {
		t.Fatalf("initial createOrUpdatePerPoolTSIGSecret() error = %v", err)
	}
	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, secretName, newKey, "new-config", "hash-2", newKeys); err != nil {
		t.Fatalf("update createOrUpdatePerPoolTSIGSecret() error = %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: instance.Namespace}, secret); err != nil {
		t.Fatalf("failed to get TSIG secret: %v", err)
	}

	if got, ok := secretStringValue(secret, "tsigkeys.conf"); !ok || got != "new-config" {
		t.Errorf("tsigkeys.conf after update = %q, ok=%v, want %q", got, ok, "new-config")
	}
	if secret.Annotations["tsigkey-id"] != "new-id" {
		t.Errorf("tsigkey-id annotation after update = %q, want %q", secret.Annotations["tsigkey-id"], "new-id")
	}
	if secret.Annotations["pool-config-hash"] != "hash-2" {
		t.Errorf("pool-config-hash annotation after update = %q, want %q", secret.Annotations["pool-config-hash"], "hash-2")
	}
}

// Test_createOrUpdateTSIGSecretWithHash_ClearsPerPoolLeftovers verifies the
// shared-mode secret writer wipes leftover per-pool artifacts (Data +
// tsig-mode annotation) when transitioning from per-pool to shared TSIG mode.
func Test_createOrUpdateTSIGSecretWithHash_ClearsPerPoolLeftovers(t *testing.T) {
	ctx := context.Background()
	r, h, instance := newTSIGTestFixtures(t)

	secretName := instance.Name + designate.TsigSecretSuffix
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: instance.Namespace,
			Labels: map[string]string{
				"service":   "designate-backendbind9",
				"component": "designate-backendbind9",
			},
			Annotations: map[string]string{
				"pool-config-hash": "stale-hash",
				"tsig-mode":        "per-pool",
			},
		},
		Data: map[string][]byte{
			"tsigkeys.conf":             []byte("stale-config"),
			designate.TSIGKeyIDsDataKey: []byte(`{"default":"stale-id"}`),
		},
	}
	if err := r.Create(ctx, existing); err != nil {
		t.Fatalf("failed to seed existing per-pool secret: %v", err)
	}

	if _, err := r.createOrUpdateTSIGSecretWithHash(ctx, h, instance, "shared-config", "shared-hash"); err != nil {
		t.Fatalf("createOrUpdateTSIGSecretWithHash() error = %v", err)
	}

	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: instance.Namespace}, secret); err != nil {
		t.Fatalf("failed to get TSIG secret: %v", err)
	}

	if _, ok := secret.Annotations["tsig-mode"]; ok {
		t.Errorf("expected tsig-mode annotation to be removed, got %+v", secret.Annotations)
	}
	if secret.Annotations["pool-config-hash"] != "shared-hash" {
		t.Errorf("pool-config-hash = %q, want %q", secret.Annotations["pool-config-hash"], "shared-hash")
	}
	if _, ok := secret.Data[designate.TSIGKeyIDsDataKey]; ok {
		t.Errorf("expected leftover per-pool data key %s to be cleared, got %+v", designate.TSIGKeyIDsDataKey, secret.Data)
	}
	if got, ok := secretStringValue(secret, "tsigkeys.conf"); !ok || got != "shared-config" {
		t.Errorf("tsigkeys.conf = %q, ok=%v, want %q", got, ok, "shared-config")
	}
}

// Test_cleanupOrphanedPerPoolTSIGSecrets_PoolRemoval covers the pool-removal
// lifecycle scenario: secrets for pools no longer present in the multipool
// config get deleted, while the canonical pool0 secret and secrets for pools
// still present are left untouched.
func Test_cleanupOrphanedPerPoolTSIGSecrets_PoolRemoval(t *testing.T) {
	ctx := context.Background()

	label := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	makeSecret := func(name string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testBackendbind9Namespace,
				Labels:    label,
			},
		}
	}

	pool0Secret := makeSecret(testBackendbind9Name + designate.TsigSecretSuffix)
	pool1Secret := makeSecret(testBackendbind9Name + "-pool1" + designate.TsigSecretSuffix)
	orphanedPool2Secret := makeSecret(testBackendbind9Name + "-pool2" + designate.TsigSecretSuffix)

	r, h, instance := newTSIGTestFixtures(t, pool0Secret, pool1Secret, orphanedPool2Secret)

	// pool2 was removed from the config; only default (pool0, implicit) and pool1 remain.
	multipoolConfig := &designate.MultipoolConfig{
		Pools: []designate.PoolConfig{
			{Name: "default"},
			{Name: "pool1"},
		},
	}

	if err := r.cleanupOrphanedPerPoolTSIGSecrets(ctx, h, instance, multipoolConfig); err != nil {
		t.Fatalf("cleanupOrphanedPerPoolTSIGSecrets() error = %v", err)
	}

	assertSecretExists := func(name string) {
		t.Helper()
		s := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: instance.Namespace}, s); err != nil {
			t.Errorf("expected secret %s to still exist, got error: %v", name, err)
		}
	}
	assertSecretDeleted := func(name string) {
		t.Helper()
		s := &corev1.Secret{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: instance.Namespace}, s)
		if err == nil {
			t.Errorf("expected secret %s to be deleted, but it still exists", name)
		} else if !k8s_errors.IsNotFound(err) {
			t.Errorf("unexpected error getting secret %s: %v", name, err)
		}
	}

	assertSecretExists(pool0Secret.Name)
	assertSecretExists(pool1Secret.Name)
	assertSecretDeleted(orphanedPool2Secret.Name)
}

// Test_getPoolConfigHash_ExcludesDefaultPool verifies the shared-TSIG hash
// (used to decide whether the shared secret needs regeneration) is driven
// only by non-default pools.
func Test_getPoolConfigHash_ExcludesDefaultPool(t *testing.T) {
	r := &DesignateBackendbind9Reconciler{}

	cfgA := &designate.MultipoolConfig{Pools: []designate.PoolConfig{
		{Name: "default", Description: "first"},
		{Name: "pool1"},
	}}
	cfgB := &designate.MultipoolConfig{Pools: []designate.PoolConfig{
		{Name: "default", Description: "changed description"},
		{Name: "pool1"},
	}}

	hashA, err := r.getPoolConfigHash(cfgA)
	if err != nil {
		t.Fatalf("getPoolConfigHash(cfgA) error = %v", err)
	}
	hashB, err := r.getPoolConfigHash(cfgB)
	if err != nil {
		t.Fatalf("getPoolConfigHash(cfgB) error = %v", err)
	}
	if hashA != hashB {
		t.Errorf("expected default pool changes to not affect shared hash: hashA=%q hashB=%q", hashA, hashB)
	}

	cfgC := &designate.MultipoolConfig{Pools: []designate.PoolConfig{
		{Name: "default"},
		{Name: "pool1"},
		{Name: "pool2"},
	}}
	hashC, err := r.getPoolConfigHash(cfgC)
	if err != nil {
		t.Fatalf("getPoolConfigHash(cfgC) error = %v", err)
	}
	if hashA == hashC {
		t.Errorf("expected adding a non-default pool to change the shared hash, got same hash %q", hashA)
	}
}

// Test_getPerPoolConfigHash_ReactsToPoolAndMdnsChanges verifies the per-pool
// hash (used to gate per-pool TSIG reconciliation) changes on pool addition
// and on mDNS IP changes, and includes the default pool.
func Test_getPerPoolConfigHash_ReactsToPoolAndMdnsChanges(t *testing.T) {
	r := &DesignateBackendbind9Reconciler{}

	cfgTwoPools := &designate.MultipoolConfig{Pools: []designate.PoolConfig{
		{Name: "default"},
		{Name: "pool1"},
	}}
	cfgThreePools := &designate.MultipoolConfig{Pools: []designate.PoolConfig{
		{Name: "default"},
		{Name: "pool1"},
		{Name: "pool2"},
	}}

	mdnsIPs := []string{"10.0.0.1"}

	hashTwoPools, err := r.getPerPoolConfigHash(cfgTwoPools, mdnsIPs)
	if err != nil {
		t.Fatalf("getPerPoolConfigHash(cfgTwoPools) error = %v", err)
	}
	hashThreePools, err := r.getPerPoolConfigHash(cfgThreePools, mdnsIPs)
	if err != nil {
		t.Fatalf("getPerPoolConfigHash(cfgThreePools) error = %v", err)
	}
	if hashTwoPools == hashThreePools {
		t.Errorf("expected pool addition to change per-pool hash, got same hash %q", hashTwoPools)
	}

	hashDifferentMdns, err := r.getPerPoolConfigHash(cfgTwoPools, []string{"10.0.0.1", "10.0.0.2"})
	if err != nil {
		t.Fatalf("getPerPoolConfigHash(cfgTwoPools, extra mdns) error = %v", err)
	}
	if hashTwoPools == hashDifferentMdns {
		t.Errorf("expected mDNS IP changes to change per-pool hash, got same hash %q", hashTwoPools)
	}
}

func Test_generateTSIGSecret(t *testing.T) {
	secretA, err := generateTSIGSecret()
	if err != nil {
		t.Fatalf("generateTSIGSecret() error = %v", err)
	}
	secretB, err := generateTSIGSecret()
	if err != nil {
		t.Fatalf("generateTSIGSecret() error = %v", err)
	}

	if secretA == secretB {
		t.Errorf("expected two calls to generateTSIGSecret() to produce different secrets")
	}

	decoded, err := base64.StdEncoding.DecodeString(secretA)
	if err != nil {
		t.Fatalf("generateTSIGSecret() produced invalid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("decoded TSIG secret length = %d, want 32", len(decoded))
	}
}
