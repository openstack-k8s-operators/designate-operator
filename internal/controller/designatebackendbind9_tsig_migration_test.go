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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Exercise the actual reconciler from legacy shared-key state through DB key
// creation and BIND rollout, retaining credentials used by the old pods.
func TestSharedTSIGMigrationReconciliation(t *testing.T) {
	ctx := context.Background()
	r, _, instance := tsigTestHelper(t, designatev1.AZModeDisabled)
	const poolID = "11111111-1111-1111-1111-111111111111"
	legacyKey := designate.TSIGKey{ID: "legacy-key", Name: designate.SharedTSIGKeyName,
		Algorithm: "hmac-sha256", Secret: "bGVnYWN5", Scope: "POOL", ResourceID: "00000000-0000-0000-0000-000000000000"} // #nosec G101 -- Synthetic TSIG test fixture.
	keys := []designate.TSIGKey{legacyKey}
	var mu sync.Mutex
	var apiURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/":
			_, _ = w.Write([]byte(`{"versions":[{"id":"v2","status":"CURRENT"}]}`))
		case req.Method == http.MethodPost && req.URL.Path == "/v3/auth/tokens":
			w.Header().Set("X-Subject-Token", "test-token")
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprintf(w, `{"token":{"expires_at":"2099-01-01T00:00:00Z","catalog":[
				{"type":"identity","name":"keystone","endpoints":[{"interface":"internal","region":"RegionOne","url":%q}]},
				{"type":"dns","name":"designate","endpoints":[{"interface":"internal","region":"RegionOne","url":%q}]}]}}`, apiURL+"/v3/", apiURL+"/")
		case req.Method == http.MethodGet && req.URL.Path == "/v2/tsigkeys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"tsigkeys": keys})
		case req.Method == http.MethodPost && req.URL.Path == "/v2/tsigkeys":
			var key designate.TSIGKey
			if err := json.NewDecoder(req.Body).Decode(&key); err != nil {
				t.Error(err)
				http.Error(w, "invalid key", http.StatusBadRequest)
				return
			}
			if key.Name != "pool1"+designate.PerPoolTSIGKeySuffix || key.Scope != "POOL" || key.ResourceID != poolID {
				t.Errorf("key must belong to the registered non-default pool: %+v", key)
			}
			key.ID = "22222222-2222-2222-2222-222222222222"
			keys = append(keys, key)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(key) // #nosec G117 -- Mock Designate response must include the synthetic TSIG secret for BIND configuration.
		case req.Method == http.MethodGet && req.URL.Path == "/api/v1/namespaces/test/pods/discovery/log":
			_, _ = fmt.Fprintf(w, "Update Pool: <Pool id:'%s' name:'pool1'>", poolID)
		default:
			t.Errorf("unexpected migration request: %s %s", req.Method, req.URL.Path)
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	apiURL = server.URL
	keystone := &keystonev1.KeystoneAPI{ObjectMeta: metav1.ObjectMeta{Name: "keystone", Namespace: instance.Namespace}}
	keystone.Spec.AdminUser = "admin"
	keystone.Spec.AdminProject = "admin"
	keystone.Spec.Region = "RegionOne"
	keystone.Spec.Secret = "keystone-password"
	keystone.Spec.PasswordSelectors.Admin = "AdminPassword"
	keystone.Status.APIEndpoints = map[string]string{"internal": apiURL + "/v3/"}
	password := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: keystone.Spec.Secret, Namespace: instance.Namespace},
		Data: map[string][]byte{"AdminPassword": []byte("test-password")}}
	canonical := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: tsigSecretNameForPool(instance.Name, 0), Namespace: instance.Namespace,
		Annotations: map[string]string{"pool-config-hash": "legacy", "tsigkey-id": legacyKey.ID}},
		Data: map[string][]byte{"tsigkeys.conf": []byte(r.generateTSIGConfig(&legacyKey, []string{"192.0.2.1"}))}}
	for _, object := range []client.Object{keystone, password, canonical} {
		if err := r.Create(ctx, object); err != nil {
			t.Fatal(err)
		}
	}
	kube, err := kubernetes.NewForConfig(&rest.Config{Host: apiURL})
	if err != nil {
		t.Fatal(err)
	}
	h, err := helper.NewHelper(instance, r.Client, kube, r.Scheme, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}
	config := &designate.MultipoolConfig{Pools: []designate.PoolConfig{{Name: "default", BindReplicas: 1}, {Name: "pool1", BindReplicas: 1}}}
	// Start discovery through reconciliation, then simulate its successful pod.
	if result, err := r.reconcileTSIGSecrets(ctx, instance, h, config); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("start migration: %+v, %v", result, err)
	}
	if waiting := getTSIGSecret(t, r, canonical.Name); waiting.Annotations["tsigkey-id"] != legacyKey.ID || string(waiting.Data["tsigkeys.conf"]) != string(canonical.Data["tsigkeys.conf"]) {
		t.Fatal("migration changed the legacy Secret before discovering registered pools")
	}
	discovery := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: "designate-pool-list", Namespace: instance.Namespace}, discovery); err != nil {
		t.Fatal(err)
	}
	discovery.Status.Succeeded = 1
	if err := r.Status().Update(ctx, discovery); err != nil {
		t.Fatal(err)
	}
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "discovery", Namespace: instance.Namespace,
		Labels: map[string]string{"job-name": discovery.Name, batchv1.ControllerUidLabel: string(discovery.UID)}}}
	if err := r.Create(ctx, pod); err != nil {
		t.Fatal(err)
	}
	// Advance past the retry cooldown before consuming the completed discovery Job.
	instance.Status.Hash["per-pool-tsig-last-attempt"] = time.Now().Add(-designate.PoolRetryCooldown).UTC().Format(time.RFC3339)
	if result, err := r.reconcileTSIGSecrets(ctx, instance, h, config); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("migrate shared key: %+v, %v", result, err)
	}
	canonical = getTSIGSecret(t, r, canonical.Name)
	if canonical.Annotations["tsig-keys-ready"] != "false" || canonical.Annotations["tsigkey-id"] != "" || len(canonical.Data["tsigkeys.conf"]) != 0 {
		t.Fatal("default pool retained a key or published new identities before BIND rollout")
	}
	secret := getTSIGSecret(t, r, tsigSecretNameForPool(instance.Name, 1))
	var ids map[string]string
	if err := json.Unmarshal(canonical.Data[designate.TSIGKeyIDsDataKey], &ids); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids["pool1"] != secret.Annotations["tsigkey-id"] || ids["pool1"] == "" {
		t.Fatalf("incorrect pool identity mapping: %v", ids)
	}
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: instance.Name + "-pool1", Namespace: instance.Namespace},
		Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{"tsig-secret-version": secret.ResourceVersion}}}}}
	if err := r.Create(ctx, sts); err != nil {
		t.Fatal(err)
	}
	if result, err := r.reconcileTSIGSecrets(ctx, instance, h, config); err != nil || result.RequeueAfter == 0 {
		t.Fatalf("must wait for BIND readiness: %+v, %v", result, err)
	}
	if getTSIGSecret(t, r, canonical.Name).Annotations["tsig-keys-ready"] != "false" {
		t.Fatal("migration released key IDs while BIND was still pending")
	}
	sts.Status = appsv1.StatefulSetStatus{ObservedGeneration: sts.Generation, ReadyReplicas: 1, UpdatedReplicas: 1, CurrentRevision: "new-keys", UpdateRevision: "new-keys"}
	if err := r.Status().Update(ctx, sts); err != nil {
		t.Fatal(err)
	}
	if result, err := r.reconcileTSIGSecrets(ctx, instance, h, config); err != nil || result != (ctrl.Result{}) {
		t.Fatalf("publish migrated keys: %+v, %v", result, err)
	}
	if getTSIGSecret(t, r, canonical.Name).Annotations["tsig-keys-ready"] != "true" {
		t.Fatal("keys were not released after BIND rollout")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 2 || keys[0] != legacyKey {
		t.Fatal("migration deleted or rotated the legacy key still used by old pods")
	}
	if !strings.Contains(string(secret.Data["tsigkeys.conf"]), keys[1].Secret) || strings.Contains(string(secret.Data["tsigkeys.conf"]), legacyKey.Name) {
		t.Fatal("BIND did not receive its registered per-pool key material")
	}
}
