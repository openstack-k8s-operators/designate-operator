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

// Multipool-specific resource management and orchestration for DesignateBackendbind9.
// This file contains all logic for managing resources (ConfigMaps, Services, StatefulSets,
// TSIG secrets) in multipool mode, including migration between single-pool and multipool modes.

package controllers

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"gopkg.in/yaml.v2"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	designatebackendbind9 "github.com/openstack-k8s-operators/designate-operator/pkg/designatebackendbind9"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

// ============================================================================
// ConfigMap Management
// ============================================================================

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

// ============================================================================
// Service Management
// ============================================================================

// reconcileMultipoolServices creates pool-specific services in multipool mode
func (r *DesignateBackendbind9Reconciler) reconcileMultipoolServices(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
	serviceLabels map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling multipool services")

	// Track which services should exist (for cleanup)
	expectedServices := make(map[string]bool)

	// Create services for each pool
	serviceIdx := 0
	for poolIdx, pool := range multipoolConfig.Pools {
		for i := 0; i < int(pool.BindReplicas); i++ {
			// Service name must match the pod name
			// Pool 0 pods: designate-backendbind9-0, designate-backendbind9-1, etc.
			// Pool 1+ pods: designate-backendbind9-pool1-0, designate-backendbind9-pool1-1, etc.
			var serviceName string
			if poolIdx == 0 {
				serviceName = fmt.Sprintf("designate-backendbind9-%d", i)
			} else {
				serviceName = fmt.Sprintf("designate-backendbind9-pool%d-%d", poolIdx, i)
			}
			expectedServices[serviceName] = true

			// Ensure we have enough Override.Services entries
			if serviceIdx >= len(instance.Spec.Override.Services) {
				err := fmt.Errorf("not enough Override.Services entries (%d) for total bind replicas (%d)", len(instance.Spec.Override.Services), serviceIdx+1)
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.CreateServiceReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.CreateServiceReadyErrorMessage,
					err.Error()))
				return ctrl.Result{}, err
			}

			svc, err := designate.CreateDNSService(
				serviceName,
				instance.Namespace,
				&instance.Spec.Override.Services[serviceIdx],
				serviceLabels,
				53,
			)
			if err != nil {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.CreateServiceReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.CreateServiceReadyErrorMessage,
					err.Error()))
				return ctrl.Result{}, err
			}

			ctrlResult, err := svc.CreateOrPatch(ctx, helper)
			if err != nil {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.CreateServiceReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.CreateServiceReadyErrorMessage,
					err.Error()))
				return ctrlResult, err
			}

			Log.Info(fmt.Sprintf("Created/updated service %s for pool %s replica %d", serviceName, pool.Name, i))
			serviceIdx++
		}
	}

	// Clean up any orphaned pool-specific services that shouldn't exist
	svcList := &corev1.ServiceList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err := helper.GetClient().List(ctx, svcList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err == nil {
		for _, svc := range svcList.Items {
			// If it's a pool-specific service and not in our expected list, delete it
			if strings.Contains(svc.Name, "-pool") && !expectedServices[svc.Name] {
				Log.Info(fmt.Sprintf("Deleting orphaned multipool service %s", svc.Name))
				err = helper.GetClient().Delete(ctx, &svc)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete orphaned service %s", svc.Name))
					return ctrl.Result{}, err
				}
			}
		}
	} else if !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	instance.Status.Conditions.MarkTrue(condition.CreateServiceReadyCondition, condition.CreateServiceReadyMessage)
	Log.Info("Reconciled multipool services successfully")
	return ctrl.Result{}, nil
}

// ============================================================================
// StatefulSet Management
// ============================================================================

// reconcileMultipoolStatefulSets creates multiple StatefulSets, one per pool
func (r *DesignateBackendbind9Reconciler) reconcileMultipoolStatefulSets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
	inputHash string,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
	topology *topologyv1.Topology,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling multipool StatefulSets")

	var totalReadyCount int32
	networkAttachmentStatus := map[string][]string{}
	allDeploymentsReady := true
	var requeueNeeded bool
	var requeueResult ctrl.Result

	// Track expected StatefulSet names for cleanup
	expectedStatefulSets := make(map[string]bool)

	// Create a StatefulSet for each pool
	for poolIdx, pool := range multipoolConfig.Pools {
		// Create a modified instance for this pool with pool-specific replicas
		poolInstance := instance.DeepCopy()
		poolInstance.Spec.Replicas = &pool.BindReplicas
		// Pool 0 (default) uses instance.Name for backwards compatibility
		// Pool 1+ use instance.Name-pool1, pool2, etc.
		var poolStatefulSetName string
		if poolIdx == 0 {
			poolStatefulSetName = instance.Name
		} else {
			poolStatefulSetName = fmt.Sprintf("%s-pool%d", instance.Name, poolIdx)
		}
		expectedStatefulSets[poolStatefulSetName] = true

		// Add pool-specific labels
		poolLabels := make(map[string]string)
		for k, v := range serviceLabels {
			poolLabels[k] = v
		}
		poolLabels["pool"] = pool.Name
		poolLabels["pool-index"] = fmt.Sprintf("%d", poolIdx)

		Log.Info(fmt.Sprintf("Creating/updating StatefulSet for pool %s with %d replicas", pool.Name, pool.BindReplicas))

		// Pool 0 uses default ConfigMap name, pool 1+ use numbered suffixes
		var poolBindIPConfigMap string
		if poolIdx == 0 {
			poolBindIPConfigMap = designate.BindPredIPConfigMap
		} else {
			poolBindIPConfigMap = fmt.Sprintf("%s-pool%d", designate.BindPredIPConfigMap, poolIdx)
		}
		deplDef, err := designatebackendbind9.StatefulSet(poolInstance, inputHash, poolLabels, serviceAnnotations, topology, poolStatefulSetName, poolBindIPConfigMap)
		if err != nil {
			return ctrl.Result{}, err
		}

		// Check if StatefulSet exists and needs to be deleted due to immutable field changes
		existingSts := &appsv1.StatefulSet{}
		err = helper.GetClient().Get(ctx, types.NamespacedName{Name: poolStatefulSetName, Namespace: poolInstance.Namespace}, existingSts)
		if err == nil {
			// Check if VolumeClaimTemplates differ (immutable field)
			if len(existingSts.Spec.VolumeClaimTemplates) > 0 && len(deplDef.Spec.VolumeClaimTemplates) > 0 {
				if existingSts.Spec.VolumeClaimTemplates[0].Name != deplDef.Spec.VolumeClaimTemplates[0].Name {
					Log.Info(fmt.Sprintf("VolumeClaimTemplate name changed, deleting StatefulSet %s for recreation", poolStatefulSetName))
					err = helper.GetClient().Delete(ctx, existingSts)
					if err != nil {
						Log.Error(err, "Failed to delete StatefulSet for recreation")
						return ctrl.Result{}, err
					}
					// Mark that we need to requeue, but continue processing other pools
					requeueNeeded = true
					requeueResult = ctrl.Result{Requeue: true}
					continue
				}
			}
		} else if !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		depl := statefulset.NewStatefulSet(
			deplDef,
			time.Duration(5)*time.Second,
		)

		ctrlResult, err := depl.CreateOrPatch(ctx, helper)
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.DeploymentReadyErrorMessage,
				err.Error()))
			return ctrlResult, err
		} else if (ctrlResult != ctrl.Result{}) {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.DeploymentReadyRunningMessage))
			// Don't return early - track that we need to requeue and continue processing
			requeueNeeded = true
			requeueResult = ctrlResult
		}

		deploy := depl.GetStatefulSet()

		// If generation doesn't match, StatefulSet is still being updated
		if deploy.Generation != deploy.Status.ObservedGeneration {
			allDeploymentsReady = false
			continue
		}
		totalReadyCount += deploy.Status.ReadyReplicas

		if err := r.verifyPoolNetworkAttachments(ctx, helper, instance, pool, &deploy, poolLabels, networkAttachmentStatus); err != nil {
			return ctrl.Result{}, err
		}

		if !statefulset.IsReady(deploy) {
			allDeploymentsReady = false
		}
	}

	// Cleanup orphaned StatefulSets (pools that were removed from config)
	// This always runs, even if pools are still being created/updated
	cleanupResult, err := r.cleanupOrphanedStatefulSets(ctx, instance, helper, expectedStatefulSets)
	if err != nil {
		return cleanupResult, err
	} else if (cleanupResult != ctrl.Result{}) {
		requeueNeeded = true
		requeueResult = cleanupResult
	}

	// Update the overall status
	instance.Status.ReadyCount = totalReadyCount
	instance.Status.NetworkAttachments = networkAttachmentStatus

	if allDeploymentsReady && len(networkAttachmentStatus) > 0 {
		instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)
	}

	if allDeploymentsReady {
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
	} else {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DeploymentReadyRunningMessage))
	}

	Log.Info(fmt.Sprintf("Reconciled multipool StatefulSets successfully, total ready: %d", totalReadyCount))

	if requeueNeeded {
		return requeueResult, nil
	}

	return ctrl.Result{}, nil
}

// verifyPoolNetworkAttachments verifies network attachments for a pool's StatefulSet
// and merges the network status into the provided networkAttachmentStatus map
func (r *DesignateBackendbind9Reconciler) verifyPoolNetworkAttachments(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	pool designate.PoolConfig,
	deploy *appsv1.StatefulSet,
	poolLabels map[string]string,
	networkAttachmentStatus map[string][]string,
) error {
	// If pool has no replicas, network verification is not needed
	if pool.BindReplicas == 0 {
		return nil
	}

	// If no pods are ready yet, skip verification
	if deploy.Status.ReadyReplicas == 0 {
		return nil
	}

	// Verify network status from pod annotations
	networkReady, poolNetworkStatus, err := nad.VerifyNetworkStatusFromAnnotation(
		ctx,
		helper,
		instance.Spec.NetworkAttachments,
		poolLabels,
		deploy.Status.ReadyReplicas,
	)
	if err != nil {
		return err
	}

	// Merge network attachment status
	for k, v := range poolNetworkStatus {
		networkAttachmentStatus[k] = v
	}

	// If network is not ready, set error condition and return
	if !networkReady {
		err := fmt.Errorf("%w: %s (pool: %s)", designate.ErrNetworkAttachmentConfig, instance.Spec.NetworkAttachments, pool.Name)
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.NetworkAttachmentsReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.NetworkAttachmentsReadyErrorMessage,
			err.Error()))
		return err
	}

	return nil
}

// cleanupOrphanedStatefulSets removes StatefulSets for pools that no longer exist in the config
func (r *DesignateBackendbind9Reconciler) cleanupOrphanedStatefulSets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	expectedStatefulSets map[string]bool,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	stsList := &appsv1.StatefulSetList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err := helper.GetClient().List(ctx, stsList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err != nil {
		Log.Error(err, "Failed to list StatefulSets for cleanup")
		return ctrl.Result{}, err
	}

	// Find orphaned StatefulSets (ones with pool label that are not in expected list)
	for _, sts := range stsList.Items {
		// Only process StatefulSets with pool label (multipool StatefulSets)
		poolName, hasPoolLabel := sts.Labels["pool"]
		if !hasPoolLabel {
			continue
		}

		// Skip if this StatefulSet is expected
		if expectedStatefulSets[sts.Name] {
			continue
		}

		Log.Info(fmt.Sprintf("Found orphaned StatefulSet %s for pool %s", sts.Name, poolName))

		// Check if this pool has active zones. If it does, designate won't allow us to remove it
		hasZones, err := r.checkPoolHasZones(ctx, helper, instance, poolName)
		if err != nil {
			Log.Error(err, fmt.Sprintf("Failed to check zones for pool %s", poolName))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.DeploymentReadyErrorMessage,
				fmt.Sprintf("Failed to check if pool %s has active zones: %v", poolName, err)))
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}

		if hasZones {
			// Pool has active zones, cannot remove
			errMsg := fmt.Sprintf("pool %s has active DNS zones and cannot be removed. Please delete zones first", poolName)
			Log.Info(errMsg)
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.DeploymentReadyErrorMessage,
				errMsg))
			return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
		}

		// No zones, proceed with graceful removal
		// First, scale to 0 if not already at 0
		if *sts.Spec.Replicas > 0 {
			Log.Info(fmt.Sprintf("Scaling down StatefulSet %s to 0 replicas before deletion", sts.Name))
			sts.Spec.Replicas = new(int32)
			err = helper.GetClient().Update(ctx, &sts)
			if err != nil {
				Log.Error(err, fmt.Sprintf("Failed to scale down StatefulSet %s", sts.Name))
				return ctrl.Result{}, err
			}
			// Requeue to allow scale-down to complete
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
		}

		// Wait until all pods are terminated
		if sts.Status.Replicas > 0 || sts.Status.ReadyReplicas > 0 {
			Log.Info(fmt.Sprintf("Waiting for StatefulSet %s pods to terminate (replicas: %d, ready: %d)",
				sts.Name, sts.Status.Replicas, sts.Status.ReadyReplicas))
			return ctrl.Result{RequeueAfter: time.Second * 10}, nil
		}

		// All pods are terminated, safe to delete StatefulSet
		Log.Info(fmt.Sprintf("Deleting orphaned StatefulSet %s for removed pool %s", sts.Name, poolName))
		err = helper.GetClient().Delete(ctx, &sts)
		if err != nil && !k8s_errors.IsNotFound(err) {
			Log.Error(err, fmt.Sprintf("Failed to delete StatefulSet %s", sts.Name))
			return ctrl.Result{}, err
		}
		// Requeue to continue cleanup if there are more orphaned StatefulSets
		return ctrl.Result{Requeue: true}, nil
	}

	return ctrl.Result{}, nil
}

// ============================================================================
// TSIG Secret Management
// ============================================================================

// reconcileTSIGSecrets creates/updates a single TSIG secret for all non-default pools in multipool mode
// TSIG (Transaction Signature) authentication is required for mdns to communicate with
// non-default pool BIND servers.
// This creates ONE secret containing TSIG keys for ALL non-default pools.
func (r *DesignateBackendbind9Reconciler) reconcileTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling TSIG secret for multipool")

	mdnsPodList := &corev1.PodList{}
	mdnsLabelSelector := map[string]string{
		"service": "designate-mdns",
	}
	err := helper.GetClient().List(ctx, mdnsPodList, client.InNamespace(instance.Namespace), client.MatchingLabels(mdnsLabelSelector))
	if err != nil {
		Log.Error(err, "Failed to list mdns pods")
		return ctrl.Result{}, err
	}

	var mdnsIPs []string
	for _, pod := range mdnsPodList.Items {
		if pod.Status.PodIP != "" {
			mdnsIPs = append(mdnsIPs, pod.Status.PodIP)
		}
	}

	Log.Info(fmt.Sprintf("Found %d mdns pod IPs for TSIG server blocks", len(mdnsIPs)))

	// Build a single TSIG configuration file containing all non-default pool keys
	// Format:
	// key "pool1-key" {
	//     algorithm hmac-sha256;
	//     secret "base64-encoded-secret";
	// };
	// key "pool2-key" {
	//     algorithm hmac-sha256;
	//     secret "base64-encoded-secret";
	// };
	// server 172.28.0.97 { keys { pool1-key; pool2-key; }; };
	// server 172.28.0.98 { keys { pool1-key; pool2-key; }; };
	var tsigConfig strings.Builder
	var tsigKeyNames []string

	// TODO: Query Designate API for TSIG keys using gophercloud or kubectl exec
	// For now, we expect the TSIG keys to already exist in the database
	// The user must create them via: openstack tsigkey create <key-name> --algorithm hmac-sha256 --resource-id <pool-uuid>
	//
	// Example query:
	// kubectl exec -n openstack openstackclient -- openstack tsigkey list -f json
	// Filter by resource_id matching pool UUID to find the correct TSIG key
	//
	// For automation, we would:
	// 1. Get pool UUID from pools.yaml
	// 2. Query: openstack tsigkey list --resource-id <pool-uuid>
	// 3. Extract name, algorithm, and secret from the response

	// Add key definitions for all non-default pools (skip pool0)
	for poolIdx, pool := range multipoolConfig.Pools {
		if poolIdx == 0 {
			// Skip default pool (pool0) - no TSIG required
			continue
		}

		// Placeholder values - in production, these MUST come from Designate database
		tsigKeyName := fmt.Sprintf("pool%d-key", poolIdx)
		tsigKeyNames = append(tsigKeyNames, tsigKeyName)
		tsigAlgorithm := "hmac-sha256"

		tsigConfig.WriteString(fmt.Sprintf("key \"%s\" {\n", tsigKeyName))
		tsigConfig.WriteString(fmt.Sprintf("    algorithm %s;\n", tsigAlgorithm))
		tsigConfig.WriteString("    secret \"TSIG_SECRET_PLACEHOLDER\";\n")
		tsigConfig.WriteString("};\n\n")

		Log.Info(fmt.Sprintf("Added TSIG key configuration for pool %s (%s)", pool.Name, tsigKeyName))
	}

	// Add server blocks for each mdns pod IP with all keys
	for _, mdnsIP := range mdnsIPs {
		tsigConfig.WriteString(fmt.Sprintf("server %s {\n", mdnsIP))
		tsigConfig.WriteString("    keys {")
		for i, keyName := range tsigKeyNames {
			if i > 0 {
				tsigConfig.WriteString(";")
			}
			tsigConfig.WriteString(fmt.Sprintf(" %s", keyName))
		}
		tsigConfig.WriteString("; };\n")
		tsigConfig.WriteString("};\n")
	}

	// Single TSIG secret name using the constant
	tsigSecretName := instance.Name + designate.TsigSecretSuffix

	// Check if secret already exists
	existingSecret := &corev1.Secret{}
	err = helper.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretName, Namespace: instance.Namespace}, existingSecret)
	secretExists := err == nil

	needsUpdate := false
	if secretExists {
		// Secret exists - check if it needs updating
		existingConfig, hasKey := existingSecret.Data["tsigkeys.conf"]
		if !hasKey || string(existingConfig) == "" {
			Log.Info(fmt.Sprintf("TSIG secret %s exists but is empty or missing tsigkeys.conf, will update", tsigSecretName))
			needsUpdate = true
		} else if strings.Contains(string(existingConfig), "TSIG_SECRET_PLACEHOLDER") {
			Log.Info(fmt.Sprintf("TSIG secret %s contains placeholder, needs manual update with real TSIG key from database", tsigSecretName))
			Log.Info("Query TSIG keys: kubectl exec -n openstack openstackclient -- openstack tsigkey list -f json")
			needsUpdate = true
		} else {
			// Secret exists and has content - check if mdns IPs or pool count changed
			currentMdnsIPCount := 0
			for _, ip := range mdnsIPs {
				if strings.Contains(string(existingConfig), fmt.Sprintf("server %s", ip)) {
					currentMdnsIPCount++
				}
			}

			currentPoolKeyCount := 0
			for _, keyName := range tsigKeyNames {
				if strings.Contains(string(existingConfig), fmt.Sprintf("key \"%s\"", keyName)) {
					currentPoolKeyCount++
				}
			}

			if currentMdnsIPCount == len(mdnsIPs) && currentPoolKeyCount == len(tsigKeyNames) {
				Log.Info(fmt.Sprintf("TSIG secret %s is up to date, skipping", tsigSecretName))
				return ctrl.Result{}, nil
			}
			Log.Info(fmt.Sprintf("TSIG secret %s needs update - mdns IPs or pool count changed", tsigSecretName))
			needsUpdate = true
		}
	} else if !k8s_errors.IsNotFound(err) {
		Log.Error(err, fmt.Sprintf("Failed to get TSIG secret %s", tsigSecretName))
		return ctrl.Result{}, err
	}

	// Create/update the single TSIG secret
	if !secretExists || needsUpdate {
		tsigSecret := &corev1.Secret{
			ObjectMeta: ctrl.ObjectMeta{
				Name:      tsigSecretName,
				Namespace: instance.Namespace,
				Labels: map[string]string{
					"service":   "designate-backendbind9",
					"component": "designate-backendbind9",
				},
			},
			Type: corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"tsigkeys.conf": tsigConfig.String(),
			},
		}

		// Set controller reference
		err = controllerutil.SetControllerReference(instance, tsigSecret, r.Scheme)
		if err != nil {
			Log.Error(err, fmt.Sprintf("Failed to set controller reference for TSIG secret %s", tsigSecretName))
			return ctrl.Result{}, err
		}

		if secretExists {
			// Update existing secret
			err = helper.GetClient().Update(ctx, tsigSecret)
			if err != nil {
				Log.Error(err, fmt.Sprintf("Failed to update TSIG secret %s", tsigSecretName))
				return ctrl.Result{}, err
			}
			Log.Info(fmt.Sprintf("Updated TSIG secret %s with %d pool keys", tsigSecretName, len(tsigKeyNames)))

			// TODO: Trigger pod restart if secret was updated
			// This is needed for the init container to mount the new secret
			// Can be done by deleting pods with pool label
		} else {
			// Create new secret
			err = helper.GetClient().Create(ctx, tsigSecret)
			if err != nil {
				Log.Error(err, fmt.Sprintf("Failed to create TSIG secret %s", tsigSecretName))
				return ctrl.Result{}, err
			}
			Log.Info(fmt.Sprintf("Created TSIG secret %s with %d pool keys (contains placeholder - needs manual update with real keys)", tsigSecretName, len(tsigKeyNames)))
		}
	}

	return ctrl.Result{}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// checkPoolHasZones checks if a pool has any active DNS zones using gophercloud
func (r *DesignateBackendbind9Reconciler) checkPoolHasZones(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	poolName string,
) (bool, error) {
	Log := r.GetLogger(ctx)

	// Verify the pool exists in the ConfigMap
	err := r.verifyPoolInConfig(ctx, helper, instance, poolName)
	if err != nil {
		Log.Info(fmt.Sprintf("Could not verify pool %s in config, assuming it's safe to remove: %v", poolName, err))
		return false, nil
	}

	// TODO: Use gophercloud to query Designate API for zones in this pool
	// Example: openstack zone list --all --long -f value | grep <poolName>
	// For now, return false to allow removal
	Log.Info(fmt.Sprintf("Pool %s zone check - using simplified check (always returns false)", poolName))
	return false, nil
}

// verifyPoolInConfig verifies that a pool exists in the pools.yaml ConfigMap
func (r *DesignateBackendbind9Reconciler) verifyPoolInConfig(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	poolName string,
) error {
	poolsConfigMap := &corev1.ConfigMap{}
	err := helper.GetClient().Get(ctx, types.NamespacedName{
		Name:      designate.PoolsYamlConfigMap,
		Namespace: instance.Namespace,
	}, poolsConfigMap)
	if err != nil {
		return fmt.Errorf("failed to get pools ConfigMap: %w", err)
	}

	poolsYamlData, ok := poolsConfigMap.Data[designate.PoolsYamlContent]
	if !ok {
		return fmt.Errorf("pools.yaml not found in ConfigMap")
	}

	var pools []designate.Pool
	if err := yaml.Unmarshal([]byte(poolsYamlData), &pools); err != nil {
		return fmt.Errorf("failed to parse pools.yaml: %w", err)
	}

	for _, pool := range pools {
		if pool.Name == poolName {
			return nil // Pool found
		}
	}

	return fmt.Errorf("pool %s not found in pools.yaml", poolName)
}
