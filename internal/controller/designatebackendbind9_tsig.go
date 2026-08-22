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
// key material into BIND, in both the shared-key (non-AZ-aware) and
// per-pool (AZ-aware) models. Everything else about multipool orchestration
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

// reconcileTSIGSecrets manages TSIG secrets for multipool mode.
// When AZAwareMode=Enabled: creates per-pool TSIG keys (one per pool including default).
// Otherwise: uses the shared TSIG key model (one key for all non-default pools).
func (r *DesignateBackendbind9Reconciler) reconcileTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	// Determine AZ-aware mode from parent Designate CR
	designateInstance, err := r.getDesignateCR(ctx, helper, instance)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get parent Designate CR for AZ mode check: %w", err)
	}

	if designateInstance.Spec.AZAwareMode == designatev1beta1.AZModeEnabled {
		return r.reconcilePerPoolTSIGSecrets(ctx, instance, helper, multipoolConfig)
	}

	return r.reconcileSharedTSIGSecret(ctx, instance, helper, multipoolConfig)
}

// reconcilePerPoolTSIGSecrets creates per-pool TSIG keys and stores them in a Secret.
// Every pool (including default) gets its own TSIG key with scope=POOL and resource_id=pool-UUID.
func (r *DesignateBackendbind9Reconciler) reconcilePerPoolTSIGSecrets(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling per-pool TSIG secrets (AZ-aware mode)")

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
	poolConfigHash, err := r.getPerPoolConfigHash(multipoolConfig, mdnsIPs)
	if err != nil {
		return ctrl.Result{}, err
	}

	existingSecret := &corev1.Secret{}
	err = helper.GetClient().Get(ctx, types.NamespacedName{Name: canonicalSecretName, Namespace: instance.Namespace}, existingSecret)
	if err == nil {
		if existingHash, exists := existingSecret.Annotations["pool-config-hash"]; exists && existingHash == poolConfigHash {
			Log.Info("Per-pool TSIG secrets are up to date (pool config unchanged)")
			return ctrl.Result{}, nil
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

	if len(tsigKeys) == 0 {
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
	for poolIdx, pool := range multipoolConfig.Pools {
		key, ok := tsigKeys[pool.Name]
		if !ok {
			// Pool not yet registered in Designate; ensurePerPoolTSIGKeys already logged this.
			continue
		}
		secretName := tsigSecretNameForPool(instance.Name, poolIdx)
		tsigConfigContent := r.generateTSIGConfig(key, mdnsIPs)
		if err := r.createOrUpdatePerPoolTSIGSecret(ctx, helper, instance, secretName, key, tsigConfigContent, poolConfigHash, tsigKeys); err != nil {
			return ctrl.Result{}, err
		}
	}

	Log.Info(fmt.Sprintf("Per-pool TSIG secrets reconciled (hash: %s, pools: %d)", poolConfigHash, len(tsigKeys)))
	return ctrl.Result{}, nil
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
		tsigSecret.StringData = stringData
		if tsigSecret.Annotations == nil {
			tsigSecret.Annotations = make(map[string]string)
		}
		tsigSecret.Annotations["pool-config-hash"] = poolConfigHash
		tsigSecret.Annotations["tsig-mode"] = "per-pool"
		tsigSecret.Annotations["tsigkey-id"] = key.ID
		return controllerutil.SetControllerReference(instance, tsigSecret, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("failed to create/update TSIG secret %s: %w", secretName, err)
	}

	return nil
}

// reconcileSharedTSIGSecret implements the original shared TSIG key model for non-AZ deployments.
func (r *DesignateBackendbind9Reconciler) reconcileSharedTSIGSecret(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	multipoolConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling shared TSIG secret for multipool")

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
		// Force re-write if per-pool artifacts are present (transition from per-pool to shared)
		_, hasPerPoolAnnotation := existingSecret.Annotations["tsig-mode"]
		_, hasPerPoolData := existingSecret.Data[designate.TSIGKeyIDsDataKey]
		needsCleanup := hasPerPoolAnnotation || hasPerPoolData

		if !needsCleanup {
			if existingHash, exists := existingSecret.Annotations["pool-config-hash"]; exists && existingHash == poolConfigHash {
				Log.Info("TSIG secret is up to date (pool config unchanged)")
				return ctrl.Result{}, nil
			}
		} else {
			Log.Info("Per-pool TSIG artifacts detected in shared mode, forcing secret re-write")
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
		if errors.Is(err, designate.ErrKeystoneNotReady) {
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
	for _, key := range slices.Sorted(maps.Keys(mdnsIPMap.Data)) {
		if strings.HasPrefix(key, "mdns_address_") && mdnsIPMap.Data[key] != "" {
			mdnsIPs = append(mdnsIPs, mdnsIPMap.Data[key])
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

// ensurePerPoolTSIGKeys creates or retrieves a TSIG key for each pool (including default).
// Returns a map of pool-name -> tsigkey-id (Designate UUID).
func (r *DesignateBackendbind9Reconciler) ensurePerPoolTSIGKeys(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	multipoolConfig *designate.MultipoolConfig,
) (map[string]*designate.TSIGKey, error) {
	Log := r.GetLogger(ctx)

	osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
	if err != nil {
		return nil, fmt.Errorf("failed to get OpenStack client: %w", err)
	}

	// Get pool name -> UUID mapping
	designateInstance, err := r.getDesignateCR(ctx, helper, instance)
	if err != nil {
		return nil, fmt.Errorf("failed to get Designate CR: %w", err)
	}

	poolNameToID, err := designate.GetPoolNameToIDMap(ctx, helper, instance.Namespace, designateInstance)
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
		keyName := pool.Name + designate.PerPoolTSIGKeySuffix

		poolUUID, exists := poolNameToID[pool.Name]
		if !exists {
			Log.Info(fmt.Sprintf("Pool %s not yet registered in Designate, skipping TSIG key creation", pool.Name))
			continue
		}

		// Check if key already exists
		if existing, ok := existingKeysByName[keyName]; ok {
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

// getPoolConfigHash generates a hash of non-default pools (for shared TSIG model).
func (r *DesignateBackendbind9Reconciler) getPoolConfigHash(multipoolConfig *designate.MultipoolConfig) (string, error) {
	var poolNames []string
	for poolIdx, pool := range multipoolConfig.Pools {
		if poolIdx > 0 { // Skip default pool (pool0)
			poolNames = append(poolNames, pool.Name)
		}
	}
	return r.computePoolConfigHash("", poolNames, nil)
}

// getPerPoolConfigHash generates a hash including ALL pools and mDNS IPs (for per-pool TSIG model).
func (r *DesignateBackendbind9Reconciler) getPerPoolConfigHash(multipoolConfig *designate.MultipoolConfig, mdnsIPs []string) (string, error) {
	var poolNames []string
	for _, pool := range multipoolConfig.Pools {
		poolNames = append(poolNames, pool.Name)
	}
	return r.computePoolConfigHash("per-pool:", poolNames, mdnsIPs)
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
		// Clear existing Data to remove any leftover keys (e.g. tsigkey-ids.json from per-pool mode)
		tsigSecret.Data = nil
		tsigSecret.StringData = map[string]string{
			"tsigkeys.conf": tsigConfigContent,
		}
		if tsigSecret.Annotations == nil {
			tsigSecret.Annotations = make(map[string]string)
		}
		tsigSecret.Annotations["pool-config-hash"] = poolConfigHash
		delete(tsigSecret.Annotations, "tsig-mode")
		return controllerutil.SetControllerReference(instance, tsigSecret, r.Scheme)
	})

	if err != nil {
		Log.Error(err, "Failed to create/update TSIG secret")
		return ctrl.Result{}, fmt.Errorf("failed to create/update TSIG secret: %w", err)
	}

	Log.Info(fmt.Sprintf("TSIG secret %s reconciled (hash: %s)", tsigSecretName, poolConfigHash))
	return ctrl.Result{}, nil
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
