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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
)

// reconcileMultipoolConfigMap translates the legacy CRD-based pool
// configuration into the equivalent multipool ConfigMap when it does not exist
// yet. Existing ConfigMaps are user-managed and are never updated here.
func (r *DesignateReconciler) reconcileMultipoolConfigMap(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
) error {
	Log := r.GetLogger(ctx)

	configMapName := types.NamespacedName{
		Name:      designate.MultipoolConfigMapName,
		Namespace: instance.Namespace,
	}

	existing := &corev1.ConfigMap{}
	err := helper.GetClient().Get(ctx, configMapName, existing)
	if err == nil {
		Log.Info(fmt.Sprintf("Multipool ConfigMap %s already exists, keeping it unchanged", configMapName.Name))
		return nil
	}
	if !k8s_errors.IsNotFound(err) {
		return fmt.Errorf("failed to get multipool ConfigMap %s: %w", configMapName.Name, err)
	}

	// If the deployment already reconciled with a multipool ConfigMap, that
	// ConfigMap is the source of truth and the CR does not describe the
	// deployed pools. Recreating it from the CR would silently replace the real
	// pool set, so report the missing ConfigMap instead.
	if previousPools := instance.Status.Hash[designate.MultipoolPoolsHashKey]; previousPools != "" {
		return fmt.Errorf("%w: %s", designate.ErrMultipoolConfigMapMissing, configMapName.Name)
	}

	// Use the same effective NS record lookup as the rest of the reconciler:
	// prefer the CR field and fall back to the legacy NS records ConfigMap.
	nsRecords, err := r.getNSRecords(ctx, helper, instance, nil)
	if err != nil {
		return fmt.Errorf("failed to read NS records for multipool ConfigMap translation: %w", err)
	}
	if len(nsRecords) == 0 {
		// Without NS records there is no valid pool to translate. Keep the
		// existing behavior and wait instead of blocking the whole deployment;
		// the translation runs once records show up.
		Log.Info("No NS records available yet, deferring multipool ConfigMap translation")
		return nil
	}

	replicas := instance.Spec.DesignateBackendbind9.Replicas
	if replicas == nil {
		return fmt.Errorf("cannot translate CRD-based pool configuration: %w", designate.ErrBindReplicasNotSet)
	}

	poolsData, err := designate.GenerateMultipoolConfigData(*replicas, nsRecords, instance.Spec.AZAwareMode)
	if err != nil {
		return fmt.Errorf("failed to translate CRD-based pool configuration to multipool ConfigMap: %w", err)
	}

	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName.Name,
			Namespace: configMapName.Namespace,
		},
		Data: map[string]string{
			designate.MultipoolConfigMapKey: poolsData,
		},
	}

	if err := controllerutil.SetControllerReference(instance, configMap, helper.GetScheme()); err != nil {
		return fmt.Errorf("failed to set owner reference on multipool ConfigMap %s: %w", configMapName.Name, err)
	}

	if err := helper.GetClient().Create(ctx, configMap); err != nil {
		// Another reconcile or a user may have created the ConfigMap after our
		// read. Never replace it in that case.
		if k8s_errors.IsAlreadyExists(err) {
			Log.Info(fmt.Sprintf("Multipool ConfigMap %s was created concurrently, keeping it unchanged", configMapName.Name))
			return nil
		}
		return fmt.Errorf("failed to create multipool ConfigMap %s: %w", configMapName.Name, err)
	}

	Log.Info(fmt.Sprintf("Created multipool ConfigMap %s from CRD-based pool configuration", configMapName.Name))
	return nil
}
