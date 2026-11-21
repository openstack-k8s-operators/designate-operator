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
	"strings"
	"testing"

	"github.com/go-logr/logr"
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
)

func multipoolMigrationTestHelper(t *testing.T, objects ...runtime.Object) (*DesignateReconciler, *helper.Helper, *designatev1.Designate) {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := designatev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}

	instance := &designatev1.Designate{
		ObjectMeta: metav1.ObjectMeta{Name: "designate", Namespace: "test", UID: "designate-uid"},
		Spec: designatev1.DesignateSpec{
			DesignateSpecBase: designatev1.DesignateSpecBase{
				NSRecords: []designatev1.DesignateNSRecord{{Hostname: "ns1.example.org.", Priority: 1}},
			},
			DesignateBackendbind9: designatev1.DesignateBackendbind9Spec{
				DesignateBackendbind9SpecBase: designatev1.DesignateBackendbind9SpecBase{
					Replicas: ptr.To(int32(2)),
				},
			},
		},
	}

	objs := append([]runtime.Object{instance}, objects...)
	r := &DesignateReconciler{Scheme: scheme}
	r.Client = fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()

	h, err := helper.NewHelper(instance, r.Client, nil, scheme, logr.Discard())
	if err != nil {
		t.Fatal(err)
	}

	return r, h, instance
}

func getTranslatedPools(t *testing.T, r *DesignateReconciler) []designate.PoolConfig {
	t.Helper()

	configMap := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{Name: designate.MultipoolConfigMapName, Namespace: "test"}, configMap)
	if err != nil {
		t.Fatalf("failed to get multipool ConfigMap: %v", err)
	}

	var pools []designate.PoolConfig
	if err := yaml.Unmarshal([]byte(configMap.Data[designate.MultipoolConfigMapKey]), &pools); err != nil {
		t.Fatalf("failed to unmarshal translated pools: %v", err)
	}
	return pools
}

func TestReconcileMultipoolConfigMapCreatesDefaultPool(t *testing.T) {
	r, h, instance := multipoolMigrationTestHelper(t)

	if err := r.reconcileMultipoolConfigMap(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pools := getTranslatedPools(t, r)
	if len(pools) != 1 {
		t.Fatalf("expected one pool, got %d", len(pools))
	}
	if pools[0].Name != designate.DefaultPoolName || pools[0].BindReplicas != 2 {
		t.Fatalf("unexpected translated pool: %+v", pools[0])
	}
	if len(pools[0].NSRecords) != 1 || pools[0].NSRecords[0].Hostname != "ns1.example.org." {
		t.Fatalf("unexpected NS records: %+v", pools[0].NSRecords)
	}
}

func TestReconcileMultipoolConfigMapKeepsExistingConfigMap(t *testing.T) {
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: designate.MultipoolConfigMapName, Namespace: "test"},
		Data:       map[string]string{designate.MultipoolConfigMapKey: "- name: default\n  bindReplicas: 7\n  nsRecords:\n    - hostname: existing.example.org.\n      priority: 1\n"},
	}
	r, h, instance := multipoolMigrationTestHelper(t, existing)

	if err := r.reconcileMultipoolConfigMap(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configMap := &corev1.ConfigMap{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: designate.MultipoolConfigMapName, Namespace: "test"}, configMap); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(configMap.Data[designate.MultipoolConfigMapKey], "existing.example.org.") {
		t.Fatalf("existing multipool ConfigMap was overwritten: %s", configMap.Data[designate.MultipoolConfigMapKey])
	}
}

func TestReconcileMultipoolConfigMapUsesLegacyNSRecords(t *testing.T) {
	legacy := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: designate.NsRecordsConfigMap, Namespace: "test"},
		Data: map[string]string{
			"ns_records": "- hostname: legacy.example.org.\n  priority: 1\n",
		},
	}
	r, h, instance := multipoolMigrationTestHelper(t, legacy)
	instance.Spec.NSRecords = nil

	if err := r.reconcileMultipoolConfigMap(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pools := getTranslatedPools(t, r)
	if len(pools) != 1 || len(pools[0].NSRecords) != 1 || pools[0].NSRecords[0].Hostname != "legacy.example.org." {
		t.Fatalf("legacy NS records were not translated: %+v", pools)
	}
}

func TestReconcileMultipoolConfigMapDefersWithoutNSRecords(t *testing.T) {
	r, h, instance := multipoolMigrationTestHelper(t)
	instance.Spec.NSRecords = nil

	if err := r.reconcileMultipoolConfigMap(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	configMap := &corev1.ConfigMap{}
	err := r.Get(context.Background(), types.NamespacedName{Name: designate.MultipoolConfigMapName, Namespace: "test"}, configMap)
	if !k8s_errors.IsNotFound(err) {
		t.Fatalf("expected no multipool ConfigMap, got error %v", err)
	}
}

func TestReconcileMultipoolConfigMapStillTranslatesWithExternalBinds(t *testing.T) {
	r, h, instance := multipoolMigrationTestHelper(t)
	instance.Spec.ExternalBindsSecret = "external-binds"

	if err := r.reconcileMultipoolConfigMap(context.Background(), instance, h); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pools := getTranslatedPools(t, r)
	if len(pools) != 1 || pools[0].Name != designate.DefaultPoolName {
		t.Fatalf("expected translated default pool, got %+v", pools)
	}
}

func TestReconcileMultipoolConfigMapReportsTranslationErrors(t *testing.T) {
	r, h, instance := multipoolMigrationTestHelper(t)
	instance.Spec.DesignateBackendbind9.Replicas = ptr.To(int32(0))

	err := r.reconcileMultipoolConfigMap(context.Background(), instance, h)
	if err == nil || !strings.Contains(err.Error(), designate.ErrInvalidBindReplicas.Error()) {
		t.Fatalf("expected invalid bind replicas error, got %v", err)
	}

	configMap := &corev1.ConfigMap{}
	getErr := r.Get(context.Background(), types.NamespacedName{Name: designate.MultipoolConfigMapName, Namespace: "test"}, configMap)
	if !k8s_errors.IsNotFound(getErr) {
		t.Fatalf("expected no multipool ConfigMap after translation error, got %v", getErr)
	}
}

func TestReconcileMultipoolConfigMapReportsDeletedConfigMapInMultipoolMode(t *testing.T) {
	r, h, instance := multipoolMigrationTestHelper(t)
	instance.Status.Hash = map[string]string{designate.MultipoolPoolsHashKey: designate.DefaultPoolName}

	err := r.reconcileMultipoolConfigMap(context.Background(), instance, h)
	if err == nil || !strings.Contains(err.Error(), designate.ErrMultipoolConfigMapMissing.Error()) {
		t.Fatalf("expected missing multipool ConfigMap error, got %v", err)
	}

	configMap := &corev1.ConfigMap{}
	getErr := r.Get(context.Background(), types.NamespacedName{Name: designate.MultipoolConfigMapName, Namespace: "test"}, configMap)
	if !k8s_errors.IsNotFound(getErr) {
		t.Fatalf("expected multipool ConfigMap not to be recreated, got %v", getErr)
	}
}
