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

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
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
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	designatebackendbind9 "github.com/openstack-k8s-operators/designate-operator/internal/designatebackendbind9"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

var (
	// ErrPoolsYamlMissing is returned when pools.yaml is not found in ConfigMap
	ErrPoolsYamlMissing = errors.New("pools.yaml not found in ConfigMap")
	// ErrPoolNotFoundInConfig is returned when a pool is not found in pools.yaml
	ErrPoolNotFoundInConfig = errors.New("pool not found in pools.yaml")
	// ErrNoDesignateCRFound is returned when no Designate CR is found in the namespace
	ErrNoDesignateCRFound = errors.New("no Designate CR found in namespace")
)

func extractPoolNameFromStatefulSetName(stsName, instanceName string) string {
	if stsName == instanceName {
		return designate.DefaultPoolName
	}
	if strings.Contains(stsName, designate.PoolStatefulSetSuffix) {
		parts := strings.Split(stsName, designate.PoolStatefulSetSuffix)
		if len(parts) == 2 {
			return "pool" + parts[1]
		}
	}
	return ""
}

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
	}
	// Single-pool mode: create default pool ConfigMap and clean up any numbered pool ConfigMaps
	return r.reconcileSinglePoolBindConfigMap(ctx, instance, helper, updatedBindMap, bindLabels)
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

			// Get override spec for this service (use empty if not configured)
			var overrideSpec service.OverrideSpec
			if serviceIdx < len(instance.Spec.Override.Services) {
				overrideSpec = instance.Spec.Override.Services[serviceIdx]
			}

			svc, err := designate.CreateDNSService(
				serviceName,
				instance.Namespace,
				&overrideSpec,
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
				// TEMPORARY: Don't fail on LoadBalancer IP timeout - just log and continue
				// This allows multipool services to be created even if MetalLB is slow to assign IPs
				Log.Info(fmt.Sprintf("Service %s created but LoadBalancer IP pending: %v", serviceName, err))
			} else if (ctrlResult != ctrl.Result{}) {
				Log.Info(fmt.Sprintf("Service %s needs requeue", serviceName))
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

	// Get TSIG secret resourceVersion to include in pod annotations for automatic restarts
	// This ensures non-default pool pods restart when TSIG keys change
	var tsigSecretResourceVersion string
	tsigSecretName := instance.Name + designate.TsigSecretSuffix
	tsigSecret := &corev1.Secret{}
	err := helper.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretName, Namespace: instance.Namespace}, tsigSecret)
	if err == nil {
		tsigSecretResourceVersion = tsigSecret.ResourceVersion
		Log.Info(fmt.Sprintf("Found TSIG secret %s with resourceVersion %s", tsigSecretName, tsigSecretResourceVersion))
	} else if !k8s_errors.IsNotFound(err) {
		Log.Error(err, "Failed to get TSIG secret for resource version")
		return ctrl.Result{}, err
	}
	// If secret doesn't exist yet (first reconciliation), tsigSecretResourceVersion will be empty - that's fine

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

		// Use base service labels for pool StatefulSets
		// Note: Pool-specific labels are intentionally not added to avoid StatefulSet
		// selector immutability issues during single-pool to multipool migrations and vice versa.
		// Pools are identified by StatefulSet name pattern instead (pool0=instance.Name, pool1+=instance.Name-pool1, etc.)

		// Add TSIG secret resourceVersion to annotations for non-default pools
		// This ensures pods restart when TSIG secret changes
		poolAnnotations := make(map[string]string)
		for k, v := range serviceAnnotations {
			poolAnnotations[k] = v
		}
		if poolIdx > 0 && tsigSecretResourceVersion != "" {
			poolAnnotations["tsig-secret-version"] = tsigSecretResourceVersion
		}

		Log.Info(fmt.Sprintf("Creating/updating StatefulSet for pool %s with %d replicas", pool.Name, pool.BindReplicas))

		// Pool 0 uses default ConfigMap name, pool 1+ use numbered suffixes
		var poolBindIPConfigMap string
		if poolIdx == 0 {
			poolBindIPConfigMap = designate.BindPredIPConfigMap
		} else {
			poolBindIPConfigMap = fmt.Sprintf("%s-pool%d", designate.BindPredIPConfigMap, poolIdx)
		}
		deplDef, err := designatebackendbind9.StatefulSet(poolInstance, inputHash, serviceLabels, poolAnnotations, topology, poolStatefulSetName, poolBindIPConfigMap)
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

		if err := r.verifyPoolNetworkAttachments(ctx, helper, instance, pool, &deploy, serviceLabels, networkAttachmentStatus); err != nil {
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
	serviceLabels map[string]string,
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
		serviceLabels,
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

	// Find orphaned StatefulSets (ones not in the expected list)
	for _, sts := range stsList.Items {
		// Identify pool StatefulSets by name pattern (pool labels are not used to avoid selector immutability issues)
		// Pool StatefulSets: instance.Name (pool0), instance.Name-pool1, instance.Name-pool2, etc.
		poolName := extractPoolNameFromStatefulSetName(sts.Name, instance.Name)

		if poolName == "" {
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
			errMsg := fmt.Sprintf("pool %s has active DNS zones and cannot be removed. Please delete zones first. Will automatically retry in 1 minute", poolName)
			Log.Info(errMsg)
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.DeploymentReadyErrorMessage,
				errMsg))
			return ctrl.Result{RequeueAfter: time.Minute}, nil
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
		Log.Info(fmt.Sprintf("StatefulSet %s deleted successfully", sts.Name))
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

	// Early exit: Check if there are any non-default pools that need TSIG
	// Pool 0 (default) doesn't need TSIG, so only pools 1+ require it
	hasNonDefaultPools := false
	for poolIdx := range multipoolConfig.Pools {
		if poolIdx > 0 {
			hasNonDefaultPools = true
			break
		}
	}

	tsigSecretName := instance.Name + designate.TsigSecretSuffix

	if !hasNonDefaultPools {
		// No non-default pools - delete TSIG secret and key from Designate
		tsigSecret := &corev1.Secret{}
		err := helper.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretName, Namespace: instance.Namespace}, tsigSecret)
		if err == nil {
			Log.Info("No non-default pools, deleting TSIG secret and key from Designate")

			// Delete TSIG key from Designate database
			osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
			if err != nil {
				Log.Error(err, "Failed to get OpenStack client for TSIG key deletion")
				// Continue with secret deletion even if we can't delete from Designate
			} else {
				err = designate.DeleteTSIGKeyByName(ctx, osclient, designate.SharedTSIGKeyName)
				if err != nil {
					Log.Error(err, "Failed to delete TSIG key from Designate")
					// Continue with secret deletion even if Designate deletion fails
				} else {
					Log.Info(fmt.Sprintf("Deleted TSIG key %s from Designate", designate.SharedTSIGKeyName))
				}
			}

			// Delete Kubernetes secret
			err = helper.GetClient().Delete(ctx, tsigSecret)
			if err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Check if we need to update the TSIG secret by comparing pool configuration hash
	// This avoids expensive operations (fetching pool IDs, querying OpenStack) when nothing has changed
	poolConfigHash, err := r.getPoolConfigHash(multipoolConfig)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Check if secret exists and has the same pool config hash
	existingSecret := &corev1.Secret{}
	err = helper.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretName, Namespace: instance.Namespace}, existingSecret)
	if err == nil {
		// Secret exists - check if pool config hash matches
		if existingHash, exists := existingSecret.Annotations["pool-config-hash"]; exists && existingHash == poolConfigHash {
			Log.Info("TSIG secret is up to date (pool config unchanged)")
			return ctrl.Result{}, nil
		}
	} else if !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Pool config has changed or secret doesn't exist - regenerate TSIG config
	Log.Info("Pool config changed or TSIG secret missing, regenerating")

	mdnsIPs, err := r.getMdnsIPsForTSIG(ctx, helper, instance.Namespace)
	if err != nil {
		// Check if mdns ConfigMap doesn't exist yet (Designate controller hasn't created it)
		if k8s_errors.IsNotFound(err) {
			Log.Info("mdns-ip-map ConfigMap not found yet, will requeue in 30 seconds")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		return ctrl.Result{}, err
	}

	// Get or create shared TSIG key for all non-default pools
	tsigKey, err := r.ensureSharedTSIGKey(ctx, helper, instance.Namespace)
	if err != nil {
		// Check if error is due to services not being ready
		if strings.Contains(err.Error(), "not ready") || strings.Contains(err.Error(), "no running") {
			Log.Info("Required services not ready for TSIG key retrieval, will requeue in 30 seconds")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		return ctrl.Result{}, err
	}

	tsigConfigContent := r.generateTSIGConfig(tsigKey, mdnsIPs)

	return r.createOrUpdateTSIGSecretWithHash(ctx, helper, instance, tsigConfigContent, poolConfigHash)
}

// getMdnsIPsForTSIG retrieves mdns pod IPs from the mdns-ip-map ConfigMap
func (r *DesignateBackendbind9Reconciler) getMdnsIPsForTSIG(
	ctx context.Context,
	helper *helper.Helper,
	namespace string,
) ([]string, error) {
	Log := r.GetLogger(ctx)

	mdnsIPMapName := fmt.Sprintf("%s-mdns-ip-map", designate.ServiceName)
	mdnsIPMap := &corev1.ConfigMap{}
	err := helper.GetClient().Get(ctx, types.NamespacedName{Name: mdnsIPMapName, Namespace: namespace}, mdnsIPMap)
	if err != nil {
		Log.Error(err, "Failed to get mdns IP ConfigMap")
		return nil, fmt.Errorf("failed to get mdns IP ConfigMap: %w", err)
	}

	// Extract IPs from ConfigMap data (mdns_address_0, mdns_address_1, etc.)
	var mdnsIPs []string
	for key, ip := range mdnsIPMap.Data {
		if strings.HasPrefix(key, "mdns_address_") && ip != "" {
			mdnsIPs = append(mdnsIPs, ip)
		}
	}
	sort.Strings(mdnsIPs) // Sort for consistent ordering

	Log.Info(fmt.Sprintf("Found %d mdns IPs for TSIG server blocks", len(mdnsIPs)))
	return mdnsIPs, nil
}

// ensureSharedTSIGKey retrieves or creates a shared TSIG key for all non-default pools
func (r *DesignateBackendbind9Reconciler) ensureSharedTSIGKey(
	ctx context.Context,
	helper *helper.Helper,
	namespace string,
) (*designate.TSIGKey, error) {
	Log := r.GetLogger(ctx)

	osclient, err := designate.GetOpenstackClient(ctx, namespace, helper)
	if err != nil {
		Log.Error(err, "Failed to get OpenStack client")
		return nil, fmt.Errorf("failed to get OpenStack client: %w", err)
	}

	// Try to get existing TSIG key
	tsigKey, err := designate.GetTSIGKeyByName(ctx, osclient, designate.SharedTSIGKeyName)
	if err != nil {
		return nil, fmt.Errorf("failed to query TSIG key: %w", err)
	}

	if tsigKey != nil {
		Log.Info(fmt.Sprintf("Using existing shared TSIG key: %s", tsigKey.Name))
		return tsigKey, nil
	}

	// Create key if it doesn't exist
	Log.Info("Creating shared TSIG key for multipool")

	// Generate random secret for TSIG key (base64-encoded 32-byte random string)
	secret, err := generateTSIGSecret()
	if err != nil {
		return nil, fmt.Errorf("failed to generate TSIG secret: %w", err)
	}

	tsigKey, err = designate.CreateTSIGKey(ctx, osclient, designate.CreateTSIGKeyOpts{
		Name:      designate.SharedTSIGKeyName,
		Algorithm: "hmac-sha256",
		Secret:    secret,
		Scope:     "POOL",
		// ResourceID is required by Designate API but only validated for UUID format,
		// not checked against actual pools. Using dummy UUID since this key is shared
		// across all non-default pools and not tied to any specific pool.
		ResourceID: "00000000-0000-0000-0000-000000000000",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create TSIG key: %w", err)
	}

	Log.Info(fmt.Sprintf("Created shared TSIG key: %s", tsigKey.Name))
	return tsigKey, nil
}

// generateTSIGConfig builds the BIND TSIG configuration file content
func (r *DesignateBackendbind9Reconciler) generateTSIGConfig(
	tsigKey *designate.TSIGKey,
	mdnsIPs []string,
) string {
	var config strings.Builder

	// Add key definition
	config.WriteString(fmt.Sprintf("key \"%s\" {\n", tsigKey.Name))
	config.WriteString(fmt.Sprintf("    algorithm %s;\n", tsigKey.Algorithm))
	config.WriteString(fmt.Sprintf("    secret \"%s\";\n", tsigKey.Secret))
	config.WriteString("};\n\n")

	// Add server blocks for each mdns IP
	for _, mdnsIP := range mdnsIPs {
		config.WriteString(fmt.Sprintf("server %s {\n", mdnsIP))
		config.WriteString(fmt.Sprintf("    keys { %s; };\n", tsigKey.Name))
		config.WriteString("};\n")
	}

	return config.String()
}

// getPoolConfigHash generates a hash of the multipool configuration.
// This hash is used to detect when pool configuration changes, avoiding unnecessary TSIG updates.
func (r *DesignateBackendbind9Reconciler) getPoolConfigHash(multipoolConfig *designate.MultipoolConfig) (string, error) {
	// Create a simple string representation of pool names (non-default pools only)
	var poolNames []string
	for poolIdx, pool := range multipoolConfig.Pools {
		if poolIdx > 0 { // Skip default pool (pool0)
			poolNames = append(poolNames, pool.Name)
		}
	}

	// Sort for consistent ordering
	sort.Strings(poolNames)

	// Hash the sorted pool names
	hashInput := strings.Join(poolNames, ",")
	hash, err := util.ObjectHash(hashInput)
	if err != nil {
		return "", fmt.Errorf("failed to hash pool config: %w", err)
	}

	return hash, nil
}

// createOrUpdateTSIGSecretWithHash creates or updates the TSIG secret with pool config hash annotation
func (r *DesignateBackendbind9Reconciler) createOrUpdateTSIGSecretWithHash(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	tsigConfigContent string,
	poolConfigHash string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	tsigSecretName := instance.Name + designate.TsigSecretSuffix

	tsigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tsigSecretName,
			Namespace: instance.Namespace,
			Labels: map[string]string{
				"service":   "designate-backendbind9",
				"component": "designate-backendbind9",
			},
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), tsigSecret, func() error {
		tsigSecret.Type = corev1.SecretTypeOpaque
		tsigSecret.StringData = map[string]string{
			"tsigkeys.conf": tsigConfigContent,
		}
		if tsigSecret.Annotations == nil {
			tsigSecret.Annotations = make(map[string]string)
		}
		tsigSecret.Annotations["pool-config-hash"] = poolConfigHash
		return controllerutil.SetControllerReference(instance, tsigSecret, r.Scheme)
	})

	if err != nil {
		Log.Error(err, "Failed to create/update TSIG secret")
		return ctrl.Result{}, fmt.Errorf("failed to create/update TSIG secret: %w", err)
	}

	Log.Info(fmt.Sprintf("TSIG secret %s reconciled (hash: %s)", tsigSecretName, poolConfigHash))
	return ctrl.Result{}, nil
}

// ============================================================================
// Helper Functions
// ============================================================================

// generateTSIGSecret generates a random base64-encoded secret for TSIG keys
func generateTSIGSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

// getDesignateCR retrieves the parent Designate CR using owner reference
func (r *DesignateBackendbind9Reconciler) getDesignateCR(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
) (*designatev1beta1.Designate, error) {
	// Find the Designate owner reference
	var designateName string
	for _, ownerRef := range instance.GetOwnerReferences() {
		if ownerRef.Kind == "Designate" {
			designateName = ownerRef.Name
			break
		}
	}

	if designateName == "" {
		return nil, ErrNoDesignateCRFound
	}

	// Get the Designate CR by name
	designate := &designatev1beta1.Designate{}
	err := helper.GetClient().Get(ctx, types.NamespacedName{
		Name:      designateName,
		Namespace: instance.Namespace,
	}, designate)
	if err != nil {
		return nil, fmt.Errorf("failed to get Designate CR %s: %w", designateName, err)
	}

	return designate, nil
}

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

	// Get the parent Designate CR - needed for pool list job
	designateInstance, err := r.getDesignateCR(ctx, helper, instance)
	if err != nil {
		Log.Info(fmt.Sprintf("Could not get Designate CR for pool validation, allowing removal to proceed: %v", err))
		return false, nil
	}

	// Get pool UUID from pool name
	poolNameToID, err := designate.GetPoolNameToIDMap(ctx, helper, instance.Namespace, designateInstance)
	if err != nil {
		// Check if error is due to pool list job failing
		if errors.Is(err, designate.ErrPoolListJobNotComplete) || errors.Is(err, designate.ErrPoolListJobFailed) {
			Log.Info("Pool list job not ready/failed, cannot check zones for pool removal - allowing removal to proceed")
			// Assume no zones and allow cleanup to proceed for now
			// The pool removal will be safe because the multipool ConfigMap has already been updated
			return false, nil
		}
		// For other errors, log but allow cleanup
		Log.Info(fmt.Sprintf("Failed to query pools for pool %s, allowing removal to proceed: %v", poolName, err))
		return false, nil
	}

	poolUUID, exists := poolNameToID[poolName]
	if !exists {
		Log.Info(fmt.Sprintf("Pool %s not found in Designate, assuming it's safe to remove", poolName))
		return false, nil
	}

	Log.Info(fmt.Sprintf("Found pool UUID %s for pool %s", poolUUID, poolName))

	// Get OpenStack client
	osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
	if err != nil {
		return false, fmt.Errorf("failed to get OpenStack client: %w", err)
	}

	// Check if pool has zones
	hasZones, err := designate.HasZonesInPool(ctx, osclient, poolUUID)
	if err != nil {
		return false, fmt.Errorf("failed to check zones for pool %s: %w", poolName, err)
	}

	if hasZones {
		count, countErr := designate.CountZonesInPool(ctx, osclient, poolUUID)
		if countErr == nil {
			Log.Info(fmt.Sprintf("Pool %s has %d active zones, cannot remove", poolName, count))
		} else {
			Log.Info(fmt.Sprintf("Pool %s has active zones, cannot remove", poolName))
		}
	} else {
		Log.Info(fmt.Sprintf("Pool %s has no active zones, safe to remove", poolName))
	}

	return hasZones, nil
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
		return ErrPoolsYamlMissing
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

	return fmt.Errorf("%w: %s", ErrPoolNotFoundInConfig, poolName)
}

// reconcileMultipoolMode orchestrates multipool vs single-pool mode handling
func (r *DesignateBackendbind9Reconciler) reconcileMultipoolMode(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	inputHash string,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
	topology *topologyv1.Topology,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("==> reconcileMultipoolMode called")

	multipoolConfig, err := designate.GetMultipoolConfig(ctx, helper.GetClient(), instance.Namespace)
	if err != nil {
		Log.Error(err, "Failed to get multipool configuration")
		return ctrl.Result{}, err
	}

	if multipoolConfig != nil {
		Log.Info(fmt.Sprintf("==> Multipool config found with %d pools, entering multipool mode", len(multipoolConfig.Pools)))
		return r.reconcileMultipoolResources(ctx, instance, helper, multipoolConfig, inputHash, serviceLabels, serviceAnnotations, topology)
	}

	Log.Info("==> No multipool config found, entering single-pool mode / cleanup")
	return r.cleanupMultipoolResources(ctx, instance, helper, inputHash, serviceLabels, serviceAnnotations, topology)
}

// reconcileMultipoolResources handles multipool mode resource creation and cleanup
func (r *DesignateBackendbind9Reconciler) reconcileMultipoolResources(
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

	// Build list of expected multipool service names
	// Pool 0 services: designate-backendbind9-0, designate-backendbind9-1, etc.
	// Pool 1+ services: designate-backendbind9-pool1-0, designate-backendbind9-pool1-1, etc.
	expectedServices := make(map[string]bool)
	for poolIdx, pool := range multipoolConfig.Pools {
		for i := 0; i < int(pool.BindReplicas); i++ {
			var serviceName string
			if poolIdx == 0 {
				serviceName = fmt.Sprintf("designate-backendbind9-%d", i)
			} else {
				serviceName = fmt.Sprintf("designate-backendbind9-pool%d-%d", poolIdx, i)
			}
			expectedServices[serviceName] = true
		}
	}

	// Clean up old services that shouldn't exist in multipool mode
	svcList := &corev1.ServiceList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err := helper.GetClient().List(ctx, svcList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	for _, svc := range svcList.Items {
		// Delete services not in our expected list (old services from previous config)
		if !expectedServices[svc.Name] {
			Log.Info(fmt.Sprintf("Deleting orphaned service %s (not in current multipool config)", svc.Name))
			err = helper.GetClient().Delete(ctx, &svc)
			if err != nil && !k8s_errors.IsNotFound(err) {
				Log.Error(err, fmt.Sprintf("Failed to delete old service %s", svc.Name))
				return ctrl.Result{}, err
			}
		}
	}

	// Pool 0 will reuse the same StatefulSet name (instance.Name)
	// Create pool-specific services
	ctrlResult, err := r.reconcileMultipoolServices(ctx, instance, helper, multipoolConfig, serviceLabels)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	ctrlResult, err = r.reconcileMultipoolStatefulSets(ctx, instance, helper, multipoolConfig, inputHash, serviceLabels, serviceAnnotations, topology)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	// Create/update TSIG secrets for non-default pools
	ctrlResult, err = r.reconcileTSIGSecrets(ctx, instance, helper, multipoolConfig)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	// Note: Per-pool bind IP ConfigMaps are cleaned up by DesignateReconciler.reconcileBindConfigMaps()

	return ctrl.Result{}, nil
}

// cleanupMultipoolResources handles cleanup during migration from multipool to single-pool mode
func (r *DesignateBackendbind9Reconciler) cleanupMultipoolResources(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	inputHash string,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
	topology *topologyv1.Topology,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("==> cleanupMultipoolResources: Starting cleanup for single-pool migration")

	// Delete any multipool services if they exist (migration from multipool to single / default pool)
	svcList := &corev1.ServiceList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err := helper.GetClient().List(ctx, svcList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err == nil {
		Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Found %d services to check for cleanup", len(svcList.Items)))
		for _, svc := range svcList.Items {
			// Delete pool-specific services (names contain "-pool")
			if strings.Contains(svc.Name, "-pool") {
				Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Deleting multipool service %s", svc.Name))
				err = helper.GetClient().Delete(ctx, &svc)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete multipool service %s", svc.Name))
					return ctrl.Result{}, err
				}
			}
		}
	} else if !k8s_errors.IsNotFound(err) {
		Log.Error(err, "==> cleanupMultipoolResources: Failed to list services")
		return ctrl.Result{}, err
	}

	// Delete numbered pool StatefulSets if they exist (pool1, pool2, etc.)
	// Pool0 (instance.Name) will be reused for single-pool mode
	stsList := &appsv1.StatefulSetList{}
	err = helper.GetClient().List(ctx, stsList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err == nil {
		Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Found %d StatefulSets to check for cleanup", len(stsList.Items)))
		for _, sts := range stsList.Items {
			Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Examining StatefulSet %s", sts.Name))
			// Identify pool StatefulSets by name pattern
			// Pool StatefulSets: instance.Name (pool0), instance.Name-pool1, instance.Name-pool2, etc.
			// Only delete numbered pool StatefulSets (pool1+), keep pool0 (instance.Name) for single-pool mode reuse
			if strings.Contains(sts.Name, designate.PoolStatefulSetSuffix) && sts.Name != instance.Name {
				Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: StatefulSet %s is a numbered pool, starting cleanup", sts.Name))
				poolName := extractPoolNameFromStatefulSetName(sts.Name, instance.Name)
				Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Extracted pool name: %s", poolName))

				// Check if this pool has active zones before deletion
				if poolName != "" {
					Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Checking if pool %s has active zones", poolName))
					hasZones, zoneCheckErr := r.checkPoolHasZones(ctx, helper, instance, poolName)
					if zoneCheckErr != nil {
						Log.Error(zoneCheckErr, fmt.Sprintf("==> cleanupMultipoolResources: Failed to check zones for pool %s", poolName))
						instance.Status.Conditions.Set(condition.FalseCondition(
							condition.DeploymentReadyCondition,
							condition.ErrorReason,
							condition.SeverityWarning,
							condition.DeploymentReadyErrorMessage,
							fmt.Sprintf("Failed to check if pool %s has active zones during migration: %v", poolName, zoneCheckErr)))
						return ctrl.Result{RequeueAfter: time.Minute}, zoneCheckErr
					}

					if hasZones {
						// Pool has active zones, cannot remove
						errMsg := fmt.Sprintf("Cannot remove pool: pool %s has active DNS zones. Please delete zones first. Will automatically retry in 1 minute", poolName)
						Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: %s", errMsg))
						instance.Status.Conditions.Set(condition.FalseCondition(
							condition.DeploymentReadyCondition,
							condition.ErrorReason,
							condition.SeverityWarning,
							condition.DeploymentReadyErrorMessage,
							errMsg))
						return ctrl.Result{RequeueAfter: time.Minute}, nil
					}
					Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Pool %s has no active zones, proceeding with deletion", poolName))
				}

				// No zones, proceed with graceful removal
				// First, scale to 0 if not already at 0
				if *sts.Spec.Replicas > 0 {
					Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Scaling down StatefulSet %s from %d to 0 replicas", sts.Name, *sts.Spec.Replicas))
					sts.Spec.Replicas = new(int32)
					err = helper.GetClient().Update(ctx, &sts)
					if err != nil {
						Log.Error(err, fmt.Sprintf("Failed to scale down StatefulSet %s", sts.Name))
						return ctrl.Result{}, err
					}
					// Requeue to allow scale-down to complete
					Log.Info("==> cleanupMultipoolResources: Requeuing to allow scale-down to complete (10s)")
					return ctrl.Result{RequeueAfter: time.Second * 10}, nil
				}

				// Wait until all pods are terminated
				if sts.Status.Replicas > 0 || sts.Status.ReadyReplicas > 0 {
					Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Waiting for StatefulSet %s pods to terminate (replicas: %d, ready: %d)",
						sts.Name, sts.Status.Replicas, sts.Status.ReadyReplicas))
					return ctrl.Result{RequeueAfter: time.Second * 10}, nil
				}

				// All pods are terminated, safe to delete StatefulSet
				Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Deleting StatefulSet %s (all pods terminated)", sts.Name))
				err = helper.GetClient().Delete(ctx, &sts)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete multipool StatefulSet %s", sts.Name))
					return ctrl.Result{}, err
				}

				// Requeue to continue cleanup if there are more orphaned StatefulSets
				Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Successfully deleted StatefulSet %s, requeuing for next pool", sts.Name))
				return ctrl.Result{Requeue: true}, nil
			}
			Log.Info(fmt.Sprintf("==> cleanupMultipoolResources: Skipping StatefulSet %s (doesn't match pool pattern or is pool0)", sts.Name))
		}
	} else if !k8s_errors.IsNotFound(err) {
		Log.Error(err, "==> cleanupMultipoolResources: Failed to list StatefulSets")
		return ctrl.Result{}, err
	}

	// Delete TSIG secret from multipool mode (only exists in multipool, not single-pool)
	// TSIG secrets have labels, so we can filter by them
	secretList := &corev1.SecretList{}
	labelSelector = map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err = helper.GetClient().List(ctx, secretList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
	if err == nil {
		for _, secret := range secretList.Items {
			// Delete TSIG secrets (names end with "-tsig")
			if strings.HasSuffix(secret.Name, designate.TsigSecretSuffix) {
				Log.Info(fmt.Sprintf("Deleting TSIG secret %s for single pool migration", secret.Name))

				// Delete TSIG key from Designate database before deleting Kubernetes secret
				osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
				if err != nil {
					Log.Error(err, "Failed to get OpenStack client for TSIG key deletion during single-pool migration")
					// Continue with secret deletion even if we can't delete from Designate
				} else {
					err = designate.DeleteTSIGKeyByName(ctx, osclient, designate.SharedTSIGKeyName)
					if err != nil {
						Log.Error(err, "Failed to delete TSIG key from Designate during single-pool migration")
						// Continue with secret deletion even if Designate deletion fails
					} else {
						Log.Info(fmt.Sprintf("Deleted TSIG key %s from Designate during single-pool migration", designate.SharedTSIGKeyName))
					}
				}

				// Delete Kubernetes secret
				err = helper.GetClient().Delete(ctx, &secret)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete TSIG secret %s", secret.Name))
					return ctrl.Result{}, err
				}
			}
		}
	} else if !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Create single-pool services for all replicas
	for i := 0; i < int(*instance.Spec.Replicas); i++ {
		var overrideSpec service.OverrideSpec
		if i < len(instance.Spec.Override.Services) {
			overrideSpec = instance.Spec.Override.Services[i]
		}

		svc, err := designate.CreateDNSService(
			fmt.Sprintf("designate-backendbind9-%d", i),
			instance.Namespace,
			&overrideSpec,
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
	}
	instance.Status.Conditions.MarkTrue(condition.CreateServiceReadyCondition, condition.CreateServiceReadyMessage)

	Log.Info("==> cleanupMultipoolResources: All multipool resources cleaned up, proceeding to reconcile single StatefulSet")
	ctrlResult, err := r.reconcileSingleStatefulSet(ctx, instance, helper, inputHash, serviceLabels, serviceAnnotations, topology)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	Log.Info("==> cleanupMultipoolResources: Successfully completed single-pool migration")
	return ctrl.Result{}, nil
}
