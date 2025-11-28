/*
Copyright 2025.

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

package controllers

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	designate "github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	ctrl "sigs.k8s.io/controller-runtime"
)

// reconcileBindConfigMaps manages all bind IP ConfigMaps lifecycle:
// - designate-bind-ip-map: represents the default pool (pool 0)
//   - In single-pool mode: contains all binds (since there's only one pool)
//   - In multipool mode: contains only pool 0 binds + RNDC key mappings
//
// - designate-bind-ip-map-pool1, pool2, etc.: Additional pools in multipool mode
// - Note: We do not create designate-bind-ip-map-pool0 because designate-bind-ip-map is pool0
func (r *DesignateReconciler) reconcileBindConfigMaps(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
	updatedBindMap map[string]string,
	bindLabels map[string]string,
) (ctrl.Result, error) {
	if multipoolConfig != nil {
		// Multipool mode: create per-pool ConfigMaps and create numbered pool ConfigMaps
		return r.reconcileMultipoolBindConfigMaps(ctx, instance, helper, multipoolConfig, updatedBindMap, bindLabels)
	} else {
		// Single-pool mode: create default pool ConfigMap and clean up any numbered pool ConfigMaps
		return r.reconcileSinglePoolBindConfigMap(ctx, instance, helper, updatedBindMap, bindLabels)
	}
}

// reconcileMultipoolBindConfigMaps creates per-pool bind IP ConfigMaps with pool-specific mappings
// - Pool 0 (default) uses designate-bind-ip-map
// - Pool 1+ use designate-bind-ip-map-pool1, pool2, etc.
func (r *DesignateReconciler) reconcileMultipoolBindConfigMaps(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
	updatedBindMap map[string]string,
	bindLabels map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	// Track which pool ConfigMaps should exist (for cleanup of orphaned ones)
	expectedConfigMaps := make(map[string]bool)

	// Create per-pool ConfigMaps with pool-local indexing
	bindIndex := 0
	for poolIdx, pool := range multipoolConfig.Pools {
		poolBindMap := make(map[string]string)

		// Map pool-local bind indexes to global bind IPs and RNDC keys
		for i := 0; i < int(pool.BindReplicas); i++ {
			// bind_address_0 in pool1 maps to bind_address_N in global map (where N = total replicas in previous pools)
			poolBindMap[fmt.Sprintf("bind_address_%d", i)] = updatedBindMap[fmt.Sprintf("bind_address_%d", bindIndex)]
			// Add RNDC key name mapping (pool-local index i maps to global rndc-key-{bindIndex})
			poolBindMap[fmt.Sprintf("rndc_key_%d", i)] = fmt.Sprintf("rndc-key-%d", bindIndex)
			bindIndex++
		}

		// Pool 0 (default pool) uses the main ConfigMap name for backward compatibility
		// Pool 1+ use numbered suffixes
		var poolConfigMapName string
		if poolIdx == 0 {
			poolConfigMapName = designate.BindPredIPConfigMap // "designate-bind-ip-map"
		} else {
			poolConfigMapName = fmt.Sprintf("%s-pool%d", designate.BindPredIPConfigMap, poolIdx)
		}
		expectedConfigMaps[poolConfigMapName] = true

		poolBindConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      poolConfigMapName,
				Namespace: instance.Namespace,
			},
		}

		_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), poolBindConfigMap, func() error {
			poolBindConfigMap.Labels = util.MergeStringMaps(poolBindConfigMap.Labels, bindLabels)
			poolBindConfigMap.Data = poolBindMap
			return controllerutil.SetControllerReference(instance, poolBindConfigMap, helper.GetScheme())
		})

		if err != nil {
			Log.Error(err, fmt.Sprintf("Unable to create ConfigMap for pool%d bind IPs", poolIdx))
			return ctrl.Result{}, err
		}
		Log.Info(fmt.Sprintf("Pool%d bind ConfigMap (%s) reconciled successfully with %d bind instances", poolIdx, poolConfigMapName, pool.BindReplicas))
	}

	// Clean up orphaned numbered pool ConfigMaps (pools removed from multipool config)
	// Only deletes pools NOT in the expected list while staying in multipool mode
	cmList := &corev1.ConfigMapList{}
	err := helper.GetClient().List(ctx, cmList, client.InNamespace(instance.Namespace), client.MatchingLabels(bindLabels))
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	for _, cm := range cmList.Items {
		// Check if this is a numbered pool ConfigMap (pool1, pool2, etc.)
		if strings.HasPrefix(cm.Name, designate.BindPredIPConfigMap+"-pool") {
			// If it's not in our expected list, it's orphaned
			if !expectedConfigMaps[cm.Name] {
				Log.Info(fmt.Sprintf("Deleting orphaned pool ConfigMap %s", cm.Name))
				err = helper.GetClient().Delete(ctx, &cm)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete orphaned ConfigMap %s", cm.Name))
					return ctrl.Result{}, err
				}
			}
		}
	}

	Log.Info("Multipool bind ConfigMaps reconciled successfully")
	return ctrl.Result{}, nil
}

// reconcileSinglePoolBindConfigMap creates the default pool ConfigMap and cleans up numbered pool ConfigMaps
func (r *DesignateReconciler) reconcileSinglePoolBindConfigMap(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	updatedBindMap map[string]string,
	bindLabels map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	// In single-pool mode, all binds go into the default ConfigMap with RNDC key mappings
	// Create the ConfigMap data with both IPs and RNDC keys
	singlePoolMap := make(map[string]string)
	for key, value := range updatedBindMap {
		singlePoolMap[key] = value
	}

	// Add RNDC key mappings (in single-pool, pod index maps directly to RNDC key)
	for i := 0; i < len(updatedBindMap); i++ {
		singlePoolMap[fmt.Sprintf("rndc_key_%d", i)] = fmt.Sprintf("rndc-key-%d", i)
	}

	// Create/update the default pool ConfigMap
	bindConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      designate.BindPredIPConfigMap,
			Namespace: instance.Namespace,
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), bindConfigMap, func() error {
		bindConfigMap.Labels = util.MergeStringMaps(bindConfigMap.Labels, bindLabels)
		bindConfigMap.Data = singlePoolMap
		return controllerutil.SetControllerReference(instance, bindConfigMap, helper.GetScheme())
	})

	if err != nil {
		Log.Error(err, "Unable to create/update default pool ConfigMap")
		return ctrl.Result{}, err
	}
	Log.Info(fmt.Sprintf("Default pool ConfigMap reconciled successfully with %d bind instances", len(updatedBindMap)))

	// Clean up all numbered pool ConfigMaps (migration from multipool to single-pool)
	// Unconditionally deletes ALL pool ConfigMaps since we're no longer in multipool mode
	cmList := &corev1.ConfigMapList{}
	err = helper.GetClient().List(ctx, cmList, client.InNamespace(instance.Namespace), client.MatchingLabels(bindLabels))
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	for _, cm := range cmList.Items {
		// Check if this is a numbered pool ConfigMap (pool1, pool2, etc.)
		if strings.HasPrefix(cm.Name, designate.BindPredIPConfigMap+"-pool") {
			Log.Info(fmt.Sprintf("Deleting numbered pool ConfigMap %s for single-pool migration", cm.Name))
			err = helper.GetClient().Delete(ctx, &cm)
			if err != nil && !k8s_errors.IsNotFound(err) {
				Log.Error(err, fmt.Sprintf("Failed to delete ConfigMap %s", cm.Name))
				return ctrl.Result{}, err
			}
		}
	}

	Log.Info("Single-pool bind ConfigMap reconciled successfully")
	return ctrl.Result{}, nil
}
