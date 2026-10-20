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

package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/go-logr/logr"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func tsigTestHelper(t *testing.T, mode designatev1.DesignateAZMode) (*DesignateBackendbind9Reconciler, *helper.Helper, *designatev1.DesignateBackendbind9) {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := designatev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := batchv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := keystonev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	instance := &designatev1.DesignateBackendbind9{ObjectMeta: metav1.ObjectMeta{
		Name: "designate-backendbind9", Namespace: "test", UID: "backend-uid",
		OwnerReferences: []metav1.OwnerReference{{APIVersion: designatev1.GroupVersion.String(), Kind: "Designate", Name: "designate"}},
	}}
	parent := &designatev1.Designate{ObjectMeta: metav1.ObjectMeta{Name: "designate", Namespace: instance.Namespace}, Spec: designatev1.DesignateSpec{DesignateSpecBase: designatev1.DesignateSpecBase{AZAwareMode: mode}}}
	mdns := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "designate-mdns-ip-map", Namespace: instance.Namespace}, Data: map[string]string{"mdns_address_0": "192.0.2.1"}}
	r := &DesignateBackendbind9Reconciler{Scheme: scheme}
	r.Client = fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&batchv1.Job{}, &appsv1.StatefulSet{}).
		WithObjects(instance, parent, mdns).Build()
	h, err := helper.NewHelper(instance, r.Client, nil, scheme, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	return r, h, instance
}

func getTSIGSecret(t *testing.T, r *DesignateBackendbind9Reconciler, name string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "test"}, secret); err != nil {
		t.Fatal(err)
	}
	return secret
}

func TestTSIGMigrationAndModeSwitch(t *testing.T) {
	for _, mode := range []designatev1.DesignateAZMode{"", designatev1.AZModeDisabled, designatev1.AZModeEnabled} {
		t.Run(string(mode), func(t *testing.T) {
			ctx := context.Background()
			r, h, instance := tsigTestHelper(t, mode)
			config := &designate.MultipoolConfig{Pools: []designate.PoolConfig{{Name: "default"}, {Name: "pool1"}, {Name: "pool2"}}}
			legacy := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tsigSecretNameForPool(instance.Name, 0), Namespace: instance.Namespace, Annotations: map[string]string{"pool-config-hash": "legacy", "tsigkey-id": "dummy-key"}}, Data: map[string][]byte{"tsigkeys.conf": []byte("legacy shared key"), designate.TSIGKeyIDsDataKey: []byte(`{"default":"dummy-key"}`)}}
			if err := r.Create(ctx, legacy); err != nil {
				t.Fatal(err)
			}
			azAware := mode == designatev1.AZModeEnabled
			hash, err := r.getPerPoolConfigHash(config, []string{"192.0.2.1"}, azAware)
			if err != nil {
				t.Fatal(err)
			}
			keys := map[string]*designate.TSIGKey{
				"pool1": {ID: "pool1-key-uuid", Name: "pool1-tsig", Algorithm: "hmac-sha256", Secret: "c2VjcmV0", Scope: "POOL", ResourceID: "11111111-1111-1111-1111-111111111111"},     // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
				"pool2": {ID: "pool2-key-uuid", Name: "pool2-tsig", Algorithm: "hmac-sha256", Secret: "c2VjcmV0Mg==", Scope: "POOL", ResourceID: "22222222-2222-2222-2222-222222222222"}, // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
			}
			if azAware {
				keys["default"] = &designate.TSIGKey{ID: "default-key-uuid", Name: "default-tsig", Algorithm: "hmac-sha256", Secret: "ZGVmYXVsdA=="} // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
			}
			// Simulate migration writes with the canonical mapping published last.
			for i := len(config.Pools) - 1; i >= 0; i-- {
				key := keys[config.Pools[i].Name]
				content := ""
				if key != nil {
					content = r.generateTSIGConfig(key, []string{"192.0.2.1"})
				}
				if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, tsigSecretNameForPool(instance.Name, i), key, content, hash, keys); err != nil {
					t.Fatal(err)
				}
			}
			canonical := getTSIGSecret(t, r, legacy.Name)
			var ids map[string]string
			if err := json.Unmarshal(canonical.Data[designate.TSIGKeyIDsDataKey], &ids); err != nil {
				t.Fatal(err)
			}
			if !azAware && (ids["default"] != "" || len(canonical.Data["tsigkeys.conf"]) != 0 || canonical.Annotations["tsigkey-id"] != "") {
				t.Fatal("default pool retained a TSIG binding or legacy key material")
			}
			for i, pool := range config.Pools[1:] {
				secret := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, i+1))
				if ids[pool.Name] != keys[pool.Name].ID {
					t.Fatalf("missing real key ID for %s", pool.Name)
				}
				content := string(secret.Data["tsigkeys.conf"])
				if !strings.Contains(content, "keys { "+keys[pool.Name].Name+"; }") || strings.Contains(content, "legacy") {
					t.Fatalf("incorrect transfer identity for %s: %s", pool.Name, content)
				}
			}
			canonical.Annotations["tsig-keys-ready"] = "true"
			if err := r.Update(ctx, canonical); err != nil {
				t.Fatal(err)
			}
			// Reconciliation must reuse migrated keys without Keystone or pool-list API calls.
			if result, err := r.reconcileTSIGSecrets(ctx, instance, h, config); err != nil || result != (ctrl.Result{}) {
				t.Fatalf("reconcile migrated secrets: %+v, %v", result, err)
			}
			missing := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, 2))
			if err := r.Delete(ctx, missing); err != nil {
				t.Fatal(err)
			}
			current, err := r.perPoolTSIGSecretsCurrent(ctx, h, instance, config, canonical, hash, azAware)
			if err != nil || current {
				t.Fatalf("missing pool Secret incorrectly accepted: current=%t err=%v", current, err)
			}
		})
	}
}

func TestDefaultOnlyTSIGNeedsNoDesignateAPI(t *testing.T) {
	r, h, instance := tsigTestHelper(t, designatev1.AZModeDisabled)
	config := &designate.MultipoolConfig{Pools: []designate.PoolConfig{{Name: "default"}}}
	if result, err := r.reconcileTSIGSecrets(context.Background(), instance, h, config); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("default-only reconciliation: %+v, %v", result, err)
	}
	canonical := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, 0))
	if len(canonical.Data["tsigkeys.conf"]) != 0 || string(canonical.Data[designate.TSIGKeyIDsDataKey]) != "{}" {
		t.Fatalf("default-only pool has TSIG material: %v", canonical.Data)
	}
}

func TestTSIGHashChangesWithAZMode(t *testing.T) {
	r := &DesignateBackendbind9Reconciler{}
	config := &designate.MultipoolConfig{Pools: []designate.PoolConfig{{Name: "default"}, {Name: "pool1"}}}
	shared, err := r.getPerPoolConfigHash(config, []string{"192.0.2.1"}, false)
	if err != nil {
		t.Fatal(err)
	}
	az, err := r.getPerPoolConfigHash(config, []string{"192.0.2.1"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if shared == az {
		t.Fatal("AZ mode transition must invalidate the TSIG cache")
	}
}

func TestTSIGMigrationWaitsForBINDRollout(t *testing.T) {
	ctx := context.Background()
	r, h, instance := tsigTestHelper(t, designatev1.AZModeDisabled)
	config := &designate.MultipoolConfig{Pools: []designate.PoolConfig{{Name: "default", BindReplicas: 1}, {Name: "pool1", BindReplicas: 1}}}
	key := &designate.TSIGKey{ID: "pool1-key", Name: "pool1-tsig", Algorithm: "hmac-sha256", Secret: "c2VjcmV0"} // #nosec G101 -- Synthetic TSIG test fixture, not a real credential.
	keys := map[string]*designate.TSIGKey{"pool1": key}
	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, tsigSecretNameForPool(instance.Name, 1), key, r.generateTSIGConfig(key, []string{"192.0.2.1"}), "new-hash", keys); err != nil {
		t.Fatal(err)
	}
	if err := r.createOrUpdatePerPoolTSIGSecret(ctx, h, instance, tsigSecretNameForPool(instance.Name, 0), nil, "", "new-hash", keys); err != nil {
		t.Fatal(err)
	}
	canonical := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, 0))
	if result, err := r.publishTSIGKeysWhenReady(ctx, h, instance, config, canonical, false); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("migration did not wait for missing StatefulSet: %+v %v", result, err)
	}
	secret := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, 1))
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name + "-pool1", Namespace: instance.Namespace, Generation: 1},
		Spec:   appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"tsig-secret-version": "old-secret-version"}}}},
		Status: appsv1.StatefulSetStatus{ObservedGeneration: 1, ReadyReplicas: 1, UpdatedReplicas: 1, CurrentRevision: "ready", UpdateRevision: "ready"},
	}
	if err := r.Create(ctx, sts); err != nil {
		t.Fatal(err)
	}
	if result, err := r.publishTSIGKeysWhenReady(ctx, h, instance, config, canonical, false); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("migration accepted old BIND key material: %+v %v", result, err)
	}
	sts.Spec.Template.Annotations["tsig-secret-version"] = secret.ResourceVersion
	if err := r.Update(ctx, sts); err != nil {
		t.Fatal(err)
	}
	if result, err := r.publishTSIGKeysWhenReady(ctx, h, instance, config, canonical, false); err != nil || result.RequeueAfter != 0 {
		t.Fatalf("migration did not publish after rollout: %+v %v", result, err)
	}
	canonical = getTSIGSecret(t, r, canonical.Name)
	if canonical.Annotations["tsig-keys-ready"] != "true" {
		t.Fatal("worker key IDs were not released after BIND rollout")
	}
}
