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

// TSIG secret management for DesignateBackendbind9 in multipool mode.
// This file owns the controller-level TSIG lifecycle: creating/rotating the
// Designate TSIG keys (via internal/designate/tsigkeys.go's Designate API
// client) and reconciling the Kubernetes Secrets that carry the resulting
// key material into BIND using one key per registered pool.
// Everything else about multipool orchestration
// (Services, StatefulSets, migration between single-pool and multipool
// modes) lives in designatebackendbind9_multipool.go.

package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
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

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

// reconcileTSIGSecrets manages per-pool TSIG secrets for multipool mode.
// Designate validates each POOL-scoped key against a registered pool and uses
// that pool ID to authorize mDNS transfers, including in non-AZ deployments.
func (r *DesignateBackendbind9Reconciler) reconcileTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	return r.reconcilePerPoolTSIGSecrets(ctx, instance, helper, multipoolConfig)
}

// reconcilePerPoolTSIGSecrets creates per-pool TSIG keys and stores them in a Secret.
// Non-default pools always need a POOL-scoped key with resource_id=pool-UUID.
// The default pool only needs a key in AZ-aware mode.
func (r *DesignateBackendbind9Reconciler) reconcilePerPoolTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling per-pool TSIG secrets")
	designateInstance, err := r.getDesignateCR(ctx, helper, instance)
	if err != nil {
		return ctrl.Result{}, err
	}
	azAware := designateInstance.Spec.AZAwareMode == designatev1beta1.AZModeEnabled

	// The pool0-named secret is canonical: besides its own pool's tsigkeys.conf, it also carries
	// the full pool-name -> tsigkey-id map that designate_controller.go reads for pools.yaml, so
	// it's used to gate the expensive Designate API calls below.
	canonicalSecretName := tsigSecretNameForPool(instance.Name, 0)

	// Get mDNS IPs — needed for hash computation and config generation
	mdnsIPs, err := r.getMdnsIPsForTSIG(ctx, helper, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			Log.Info("mdns-ip-map ConfigMap not found yet, will requeue in 30 seconds")
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		return ctrl.Result{}, err
	}

	// Check pool config hash to avoid unnecessary work
	poolConfigHash, err := r.getPerPoolConfigHash(multipoolConfig, mdnsIPs, azAware)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingSecret := &corev1.Secret{}
	err = helper.GetClient().Get(ctx, types.NamespacedName{Name: canonicalSecretName, Namespace: instance.Namespace}, existingSecret)
	if err == nil {
		current, checkErr := r.perPoolTSIGSecretsCurrent(ctx, helper, instance, multipoolConfig, existingSecret, poolConfigHash, azAware)
		if checkErr != nil {
			return ctrl.Result{}, checkErr
		}
		if current {
			return r.publishTSIGKeysWhenReady(ctx, helper, instance, multipoolConfig, existingSecret, azAware)
		}
	} else if !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// Create per-pool TSIG keys in Designate
	tsigKeys, err := r.ensurePerPoolTSIGKeys(ctx, helper, instance, multipoolConfig)
	if err != nil {
		if errors.Is(err, designate.ErrKeystoneNotReady) ||
			errors.Is(err, designate.ErrPoolListJobNotComplete) || errors.Is(err, designate.ErrPoolListJobFailed) ||
			errors.Is(err, designate.ErrNoPoolsFound) {
			Log.Info(fmt.Sprintf("Dependencies not ready for per-pool TSIG key creation, will requeue in 30 seconds: %v", err))
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		return ctrl.Result{}, err
	}

	if len(tsigKeys) == 0 && (azAware || len(multipoolConfig.Pools) > 1) {
		Log.Info("No pools registered in Designate yet, will requeue in 30 seconds")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// Clean up TSIG keys/secrets for pools that were removed from the config
	if cleanupErr := r.cleanupOrphanedPerPoolTSIGKeys(ctx, helper, instance, multipoolConfig); cleanupErr != nil {
		Log.Error(cleanupErr, "Failed to cleanup orphaned per-pool TSIG keys (non-fatal)")
	}
	if cleanupErr := r.cleanupOrphanedPerPoolTSIGSecrets(ctx, helper, instance, multipoolConfig); cleanupErr != nil {
		Log.Error(cleanupErr, "Failed to cleanup orphaned per-pool TSIG secrets (non-fatal)")
	}

	// Each pool is a separate BIND process (separate StatefulSet), so it only ever needs its own
	// key — write one Secret per pool rather than merging every pool's key into one file (BIND's
	// "server" clause only accepts a single key per remote address).
	// Publish the canonical mapping LAST. A failed write to any pool's Secret must
	// not leave a matching hash that suppresses reconciliation on the next retry.
	for poolIdx := len(multipoolConfig.Pools) - 1; poolIdx >= 0; poolIdx-- {
		pool := multipoolConfig.Pools[poolIdx]
		key := tsigKeys[pool.Name]
		tsigConfigContent := ""
		if key != nil {
			tsigConfigContent = r.generateTSIGConfig(key, mdnsIPs)
		}
		secretName := tsigSecretNameForPool(instance.Name, poolIdx)
		if err := r.createOrUpdatePerPoolTSIGSecret(ctx, helper, instance, secretName, key, tsigConfigContent, poolConfigHash, tsigKeys); err != nil {
			return ctrl.Result{}, err
		}
	}
	// Do not delete the legacy shared DB key here: pods from an older operator
	// may still use it during rollout. It is never assigned to a pool again.

	Log.Info(fmt.Sprintf("Per-pool TSIG secrets reconciled (hash: %s, pools: %d)", poolConfigHash, len(tsigKeys)))
	// Let the StatefulSet controller roll out the new key material before workers
	// start signing SOA checks with the newly published IDs.
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// publishTSIGKeysWhenReady prevents workers from using new keys before BIND has
// loaded them. The legacy DB key stays valid while old BIND pods are running.
func (r *DesignateBackendbind9Reconciler) publishTSIGKeysWhenReady(
	ctx context.Context, h *helper.Helper, instance *designatev1beta1.DesignateBackendbind9,
	config *designate.MultipoolConfig, canonical *corev1.Secret, azAware bool,
) (ctrl.Result, error) {
	if canonical.Annotations["tsig-keys-ready"] == "true" {
		return ctrl.Result{}, nil
	}
	for i, pool := range config.Pools {
		if pool.Name == designate.DefaultPoolName && !azAware {
			continue
		}
		secret := &corev1.Secret{}
		if err := h.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretNameForPool(instance.Name, i), Namespace: instance.Namespace}, secret); err != nil {
			return ctrl.Result{}, err
		}
		name := strings.TrimSuffix(secret.Name, designate.TsigSecretSuffix)
		sts := &appsv1.StatefulSet{}
		err := h.GetClient().Get(ctx, types.NamespacedName{Name: name, Namespace: instance.Namespace}, sts)
		if k8s_errors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		if err != nil {
			return ctrl.Result{}, err
		}
		if sts.Spec.Template.Annotations["tsig-secret-version"] != secret.ResourceVersion ||
			sts.Status.ObservedGeneration != sts.Generation || sts.Status.ReadyReplicas != pool.BindReplicas ||
			sts.Status.UpdatedReplicas != pool.BindReplicas || sts.Status.CurrentRevision != sts.Status.UpdateRevision {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
	}
	base := canonical.DeepCopy()
	canonical.Annotations["tsig-keys-ready"] = "true"
	if err := h.GetClient().Patch(ctx, canonical, client.MergeFrom(base)); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// perPoolTSIGSecretsCurrent checks every Secret before trusting the canonical hash.
// This also recovers from partial writes and deleted per-pool Secrets.
func (r *DesignateBackendbind9Reconciler) perPoolTSIGSecretsCurrent(
	ctx context.Context, h *helper.Helper, instance *designatev1beta1.DesignateBackendbind9,
	config *designate.MultipoolConfig, canonical *corev1.Secret, hash string, azAware bool,
) (bool, error) {
	if canonical.Annotations["pool-config-hash"] != hash || canonical.Annotations["tsig-mode"] != "per-pool" {
		return false, nil
	}
	var ids map[string]string
	if err := json.Unmarshal(canonical.Data[designate.TSIGKeyIDsDataKey], &ids); err != nil {
		return false, nil
	}
	if !azAware && (ids[designate.DefaultPoolName] != "" || len(canonical.Data["tsigkeys.conf"]) != 0 || canonical.Annotations["tsigkey-id"] != "") {
		return false, nil
	}
	for i, pool := range config.Pools {
		if pool.Name == designate.DefaultPoolName && !azAware {
			continue
		}
		secret := &corev1.Secret{}
		err := h.GetClient().Get(ctx, types.NamespacedName{Name: tsigSecretNameForPool(instance.Name, i), Namespace: instance.Namespace}, secret)
		if k8s_errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if ids[pool.Name] == "" || secret.Annotations["tsigkey-id"] != ids[pool.Name] || secret.Annotations["pool-config-hash"] != hash || len(secret.Data["tsigkeys.conf"]) == 0 {
			return false, nil
		}
	}
	return true, nil
}

// tsigSecretNameForPool computes the per-pool TSIG Secret name for a given pool index. Every
// pool gets its own Secret containing only its own key — pool0 keeps the base name for
// backwards compatibility, matching the naming convention used elsewhere for per-pool
// StatefulSets/ConfigMaps (e.g. poolBindIPConfigMap, poolStatefulSetName), and matching the
// tsigSecretName derivation in designatebackendbind9.StatefulSet().
func tsigSecretNameForPool(instanceName string, poolIdx int) string {
	if poolIdx == 0 {
		return instanceName + designate.TsigSecretSuffix
	}
	return fmt.Sprintf("%s-pool%d%s", instanceName, poolIdx, designate.TsigSecretSuffix)
}

// createOrUpdatePerPoolTSIGSecret writes a single pool's TSIG config to its own Secret. The
// canonical pool0-named secret additionally carries the full pool-name -> tsigkey-id map that
// designate_controller.go reads to populate pools.yaml's tsigkey_id fields.
func (r *DesignateBackendbind9Reconciler) createOrUpdatePerPoolTSIGSecret(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	secretName string,
	key *designate.TSIGKey,
	tsigConfigContent string,
	poolConfigHash string,
	allTsigKeys map[string]*designate.TSIGKey,
) error {
	tsigSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: instance.Namespace,
			Labels: map[string]string{
				"service":   "designate-backendbind9",
				"component": "designate-backendbind9",
			},
		},
	}

	_, err := controllerutil.CreateOrPatch(ctx, helper.GetClient(), tsigSecret, func() error {
		tsigSecret.Type = corev1.SecretTypeOpaque
		stringData := map[string]string{
			"tsigkeys.conf": tsigConfigContent,
		}
		if secretName == tsigSecretNameForPool(instance.Name, 0) {
			tsigKeyIDMapping := make(map[string]string)
			for poolName, k := range allTsigKeys {
				tsigKeyIDMapping[poolName] = k.ID
			}
			tsigKeyIDsJSON, err := json.Marshal(tsigKeyIDMapping)
			if err != nil {
				return fmt.Errorf("failed to marshal tsigkey_id mapping: %w", err)
			}
			stringData[designate.TSIGKeyIDsDataKey] = string(tsigKeyIDsJSON)
		}
		// Write Data directly so removed values really disappear, including legacy
		// shared key material on the canonical (default pool) Secret.
		tsigSecret.Data = make(map[string][]byte, len(stringData))
		for name, value := range stringData {
			tsigSecret.Data[name] = []byte(value)
		}
		tsigSecret.StringData = nil
		if tsigSecret.Annotations == nil {
			tsigSecret.Annotations = make(map[string]string)
		}
		tsigSecret.Annotations["pool-config-hash"] = poolConfigHash
		tsigSecret.Annotations["tsig-mode"] = "per-pool"
		if secretName == tsigSecretNameForPool(instance.Name, 0) {
			tsigSecret.Annotations["tsig-keys-ready"] = "false"
		}
		delete(tsigSecret.Annotations, "tsigkey-id")
		if key != nil {
			tsigSecret.Annotations["tsigkey-id"] = key.ID
		}
		return controllerutil.SetControllerReference(instance, tsigSecret, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to create/update TSIG secret %s: %w", secretName, err)
	}

	return nil
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
	for _, key := range slices.Sorted(maps.Keys(mdnsIPMap.Data)) {
		if strings.HasPrefix(key, "mdns_address_") && mdnsIPMap.Data[key] != "" {
			mdnsIPs = append(mdnsIPs, mdnsIPMap.Data[key])
		}
	}
	sort.Strings(mdnsIPs) // Sort for consistent ordering

	Log.Info(fmt.Sprintf("Found %d mdns IPs for TSIG server blocks", len(mdnsIPs)))
	return mdnsIPs, nil
}

// ensurePerPoolTSIGKeys creates or retrieves keys for pools that need signed transfers.
// Returns a map of pool-name -> tsigkey-id (Designate UUID).
func (r *DesignateBackendbind9Reconciler) ensurePerPoolTSIGKeys(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	multipoolConfig *designate.MultipoolConfig,
) (map[string]*designate.TSIGKey, error) {
	Log := r.GetLogger(ctx)

	designateInstance, err := r.getDesignateCR(ctx, helper, instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get Designate CR: %w", err)
	}
	azAware := designateInstance.Spec.AZAwareMode == designatev1beta1.AZModeEnabled
	if !azAware && len(multipoolConfig.Pools) == 1 && multipoolConfig.Pools[0].Name == designate.DefaultPoolName {
		return map[string]*designate.TSIGKey{}, nil
	}
	osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenStack client: %w", err)
	}

	var requiredPools []string
	for _, pool := range multipoolConfig.Pools {
		if pool.Name != designate.DefaultPoolName || azAware {
			requiredPools = append(requiredPools, pool.Name)
		}
	}
	poolNameToID, err := designate.GetPoolNameToIDMap(ctx, helper, instance.Namespace, designateInstance, requiredPools...)
	if err != nil {
		return nil, fmt.Errorf("failed to get pool name-to-ID mapping: %w", err)
	}

	// Get all existing TSIG keys to avoid duplicate creation
	existingKeys, err := designate.ListAllTSIGKeys(ctx, osclient)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing TSIG keys: %w", err)
	}

	existingKeysByName := make(map[string]*designate.TSIGKey)
	for i := range existingKeys {
		existingKeysByName[existingKeys[i].Name] = &existingKeys[i]
	}

	result := make(map[string]*designate.TSIGKey)

	for _, pool := range multipoolConfig.Pools {
		if pool.Name == designate.DefaultPoolName && !azAware {
			continue
		}
		keyName := pool.Name + designate.PerPoolTSIGKeySuffix

		poolUUID, exists := poolNameToID[pool.Name]
		if !exists || poolUUID == "" {
			return nil, fmt.Errorf("%w: pool %s is not registered yet", designate.ErrNoPoolsFound, pool.Name)
		}

		// Check if key already exists
		if existing, ok := existingKeysByName[keyName]; ok {
			// Repair a stale binding without rotating the name, ID, or secret used
			// by running BIND pods.
			if err := designate.EnsureTSIGKeyPool(ctx, osclient, existing, poolUUID); err != nil {
				return nil, err
			}
			Log.Info(fmt.Sprintf("Using existing per-pool TSIG key for pool %s: %s (ID: %s)", pool.Name, keyName, existing.ID))
			result[pool.Name] = existing
			continue
		}

		// Create new per-pool TSIG key
		secret, err := generateTSIGSecret()
		if err != nil {
			return nil, fmt.Errorf("failed to generate TSIG secret for pool %s: %w", pool.Name, err)
		}

		tsigKey, err := designate.CreateTSIGKey(ctx, osclient, designate.CreateTSIGKeyOpts{
			Name:       keyName,
			Algorithm:  "hmac-sha256",
			Secret:     secret,
			Scope:      "POOL",
			ResourceID: poolUUID,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to create TSIG key for pool %s: %w", pool.Name, err)
		}

		Log.Info(fmt.Sprintf("Created per-pool TSIG key for pool %s: %s (ID: %s)", pool.Name, keyName, tsigKey.ID))
		result[pool.Name] = tsigKey
	}

	return result, nil
}

// cleanupOrphanedPerPoolTSIGKeys deletes TSIG keys from Designate for pools that no longer exist in the config.
func (r *DesignateBackendbind9Reconciler) cleanupOrphanedPerPoolTSIGKeys(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	multipoolConfig *designate.MultipoolConfig,
) error {
	Log := r.GetLogger(ctx)

	osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
	if err != nil {
		return fmt.Errorf("failed to get OpenStack client for TSIG cleanup: %w", err)
	}

	existingKeys, err := designate.ListAllTSIGKeys(ctx, osclient)
	if err != nil {
		return fmt.Errorf("failed to list TSIG keys for cleanup: %w", err)
	}

	// Build set of expected per-pool key names
	expectedKeys := make(map[string]bool)
	for _, pool := range multipoolConfig.Pools {
		expectedKeys[pool.Name+designate.PerPoolTSIGKeySuffix] = true
	}

	// Delete keys that have the per-pool suffix but aren't in the expected set
	for _, key := range existingKeys {
		if strings.HasSuffix(key.Name, designate.PerPoolTSIGKeySuffix) && !expectedKeys[key.Name] {
			Log.Info(fmt.Sprintf("Deleting orphaned per-pool TSIG key: %s (ID: %s)", key.Name, key.ID))
			err := designate.DeleteTSIGKeyByName(ctx, osclient, key.Name)
			if err != nil {
				Log.Error(err, fmt.Sprintf("Failed to delete orphaned TSIG key %s", key.Name))
			}
		}
	}

	return nil
}

// cleanupOrphanedPerPoolTSIGSecrets deletes per-pool TSIG Secrets for pools that no longer exist
// in the config (mirrors cleanupOrphanedPerPoolTSIGKeys, but for the k8s Secrets rather than the
// Designate-side keys).
func (r *DesignateBackendbind9Reconciler) cleanupOrphanedPerPoolTSIGSecrets(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	multipoolConfig *designate.MultipoolConfig,
) error {
	Log := r.GetLogger(ctx)

	// Pool0 keeps the base secret name and is always present, so it's never orphaned — only
	// poolN>0 secrets (named "<instance.Name>-poolN<TsigSecretSuffix>") can become orphaned.
	expectedSecrets := make(map[string]bool)
	for poolIdx := range multipoolConfig.Pools {
		if poolIdx > 0 {
			expectedSecrets[tsigSecretNameForPool(instance.Name, poolIdx)] = true
		}
	}

	secretList := &corev1.SecretList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	if err := helper.GetClient().List(ctx, secretList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector)); err != nil {
		return fmt.Errorf("failed to list TSIG secrets for cleanup: %w", err)
	}

	poolSecretPrefix := instance.Name + "-pool"
	for i := range secretList.Items {
		secret := &secretList.Items[i]
		isPoolTsigSecret := strings.HasPrefix(secret.Name, poolSecretPrefix) && strings.HasSuffix(secret.Name, designate.TsigSecretSuffix)
		if !isPoolTsigSecret || expectedSecrets[secret.Name] {
			continue
		}
		Log.Info(fmt.Sprintf("Deleting orphaned per-pool TSIG secret: %s", secret.Name))
		if err := helper.GetClient().Delete(ctx, secret); err != nil && !k8s_errors.IsNotFound(err) {
			Log.Error(err, fmt.Sprintf("Failed to delete orphaned TSIG secret %s", secret.Name))
		}
	}

	return nil
}

// generateTSIGConfig builds the BIND TSIG configuration file content
func (r *DesignateBackendbind9Reconciler) generateTSIGConfig(
	tsigKey *designate.TSIGKey,
	mdnsIPs []string,
) string {
	var config strings.Builder

	if !designate.IsValidTSIGKeyName(tsigKey.Name) {
		return ""
	}

	// Add key definition
	fmt.Fprintf(&config, "key \"%s\" {\n", tsigKey.Name)
	fmt.Fprintf(&config, "    algorithm %s;\n", tsigKey.Algorithm)
	fmt.Fprintf(&config, "    secret \"%s\";\n", tsigKey.Secret)
	config.WriteString("};\n\n")

	// Add server blocks for each mdns IP
	for _, mdnsIP := range mdnsIPs {
		fmt.Fprintf(&config, "server %s {\n", mdnsIP)
		fmt.Fprintf(&config, "    keys { %s; };\n", tsigKey.Name)
		config.WriteString("};\n")
	}

	return config.String()
}

// computePoolConfigHash is a shared helper that sorts pool names and computes a hash.
func (r *DesignateBackendbind9Reconciler) computePoolConfigHash(prefix string, poolNames []string, mdnsIPs []string) (string, error) {
	sort.Strings(poolNames)
	hashInput := prefix + strings.Join(poolNames, ",")
	if len(mdnsIPs) > 0 {
		hashInput += "|mdns:" + strings.Join(mdnsIPs, ",")
	}
	hash, err := util.ObjectHash(hashInput)
	if err != nil {
		return "", fmt.Errorf("failed to hash pool config: %w", err)
	}
	return hash, nil
}

// getPerPoolConfigHash generates a hash including ALL pools and mDNS IPs (for per-pool TSIG model).
func (r *DesignateBackendbind9Reconciler) getPerPoolConfigHash(multipoolConfig *designate.MultipoolConfig, mdnsIPs []string, azAware bool) (string, error) {
	var poolNames []string
	for _, pool := range multipoolConfig.Pools {
		poolNames = append(poolNames, pool.Name)
	}
	return r.computePoolConfigHash(fmt.Sprintf("per-pool-v2:az=%t:", azAware), poolNames, mdnsIPs)
}

// generateTSIGSecret generates a random base64-encoded secret for TSIG keys
func generateTSIGSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(bytes), nil
}

// cleanupMultipoolTSIGSecrets deletes TSIG secrets used in multipool mode during single-pool migration
func (r *DesignateBackendbind9Reconciler) cleanupMultipoolTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
) error {
	Log := r.GetLogger(ctx)

	secretList := &corev1.SecretList{}
	labelSelector := map[string]string{
		"service":   "designate-backendbind9",
		"component": "designate-backendbind9",
	}
	err := helper.GetClient().List(ctx, secretList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
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
				if err := helper.GetClient().Delete(ctx, &secret); err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete TSIG secret %s", secret.Name))
					return err
				}
			}
		}
	} else if !k8s_errors.IsNotFound(err) {
		return err
	}
	return nil
}
