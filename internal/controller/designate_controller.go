/*
Copyright 2022.

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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v2"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	rabbitmqv1 "github.com/openstack-k8s-operators/infra-operator/apis/rabbitmq/v1beta1"
	redisv1 "github.com/openstack-k8s-operators/infra-operator/apis/redis/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/job"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	oko_secret "github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// getOrDefault - helper for setting 'default' value if empty value in CR.
func getOrDefault(source string, def string) string {
	if len(source) == 0 {
		return def
	}
	return source
}

// helper function for retrieving a secret.
func getSecret(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	conditions *condition.Conditions,
	secretName string,
	envVars *map[string]env.Setter,
	prefix string,
) (ctrl.Result, error) {
	secret, hash, err := oko_secret.GetSecret(ctx, h, secretName, namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Since service secrets should have been manually created by the user and referenced in the spec,
			// we treat this as a warning because it means that the service will not be able to start.
			h.GetLogger().Info(fmt.Sprintf("Secret %s not found", secretName))
			conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.InputReadyWaitingMessage))
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}
		conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	// Add a prefix to the var name to avoid accidental collision with other non-secret
	// vars. The secret names themselves will be unique.
	(*envVars)[prefix+secret.Name] = env.SetValue(hash)

	return ctrl.Result{}, nil
}

// GetClient -
func (r *DesignateReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *DesignateReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetLogger returns a logger object with a prefix of "controller.name" and additional controller context fields
func (r *DesignateReconciler) GetLogger(ctx context.Context) logr.Logger {
	return log.FromContext(ctx).WithName("Controllers").WithName("Designate")
}

// GetScheme -
func (r *DesignateReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// DesignateReconciler reconciles a Designate object
type DesignateReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// getMultipoolConfig is a helper method that retrieves multipool configuration
// with consistent error handling and logging
func (r *DesignateReconciler) getMultipoolConfig(
	ctx context.Context,
	k8sClient client.Client,
	namespace string,
) (*designate.MultipoolConfig, error) {
	Log := r.GetLogger(ctx)

	multipoolConfig, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
	if err != nil {
		Log.Error(err, "Failed to get multipool configuration")
		return nil, err
	}

	return multipoolConfig, nil
}

// validatePoolRemovals checks if any pools have been removed from the multipool config
// and validates that removed pools don't have active DNS zones before allowing the removal
func (r *DesignateReconciler) validatePoolRemovals(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	currentConfig *designate.MultipoolConfig,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	// Get the previous pool list from status.Hash
	previousPoolsStr, exists := instance.Status.Hash["multipool-pools"]
	if !exists {
		// First time seeing multipool config, no removals to validate
		return ctrl.Result{}, nil
	}

	// Parse previous pool list (stored as comma-separated names)
	var previousPools []string
	if previousPoolsStr != "" {
		previousPools = strings.Split(previousPoolsStr, ",")
	}

	// Build current pool list
	var currentPools []string
	if currentConfig != nil {
		for _, pool := range currentConfig.Pools {
			currentPools = append(currentPools, pool.Name)
		}
	}

	// Find removed pools (in previous but not in current)
	currentPoolSet := make(map[string]bool)
	for _, pool := range currentPools {
		currentPoolSet[pool] = true
	}

	var removedPools []string
	for _, pool := range previousPools {
		if !currentPoolSet[pool] {
			removedPools = append(removedPools, pool)
		}
	}

	if len(removedPools) == 0 {
		// No pools removed, validation passed
		return ctrl.Result{}, nil
	}

	Log.Info(fmt.Sprintf("Detected pool removal: %v - checking for active zones", removedPools))

	// Get pool name -> UUID mapping once for all pools
	poolNameToID, err := designate.GetPoolNameToIDMap(ctx, helper, instance.Namespace, instance)
	if err != nil {
		// Check if error is due to pool list job not completing or failing
		if errors.Is(err, designate.ErrPoolListJobNotComplete) || errors.Is(err, designate.ErrPoolListJobFailed) {
			Log.Info("Pool list job not ready, skipping pool removal validation (will retry in 30 seconds)")
			// Don't set error condition, just requeue - waiting for job to complete or succeed on retry
			return ctrl.Result{RequeueAfter: time.Second * 30}, nil
		}
		Log.Error(err, "Failed to get pool UUID mapping")
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			fmt.Sprintf("Cannot validate pool removal: failed to get pool UUID mapping: %v", err)))
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	// Get OpenStack client for zone checking
	osclient, err := designate.GetOpenstackClient(ctx, instance.Namespace, helper)
	if err != nil {
		Log.Error(err, "Failed to get OpenStack client for pool removal validation")
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			fmt.Sprintf("Cannot validate pool removal: failed to get OpenStack client: %v", err)))
		return ctrl.Result{RequeueAfter: time.Minute}, err
	}

	for _, poolName := range removedPools {
		// Look up pool UUID from map
		poolUUID, exists := poolNameToID[poolName]
		if !exists {
			// Pool might not exist in Designate yet (e.g., never synced), allow removal
			Log.Info(fmt.Sprintf("Pool %s not found in Designate database, allowing removal", poolName))
			continue
		}

		// Check if pool has zones
		hasZones, err := designate.HasZonesInPool(ctx, osclient, poolUUID)
		if err != nil {
			Log.Error(err, fmt.Sprintf("Failed to check zones for pool %s (UUID: %s)", poolName, poolUUID))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.InputReadyErrorMessage,
				fmt.Sprintf("Cannot validate pool removal: failed to check zones for pool %s: %v", poolName, err)))
			return ctrl.Result{RequeueAfter: time.Minute}, err
		}

		if hasZones {
			// Count zones for better error message
			count, countErr := designate.CountZonesInPool(ctx, osclient, poolUUID)
			var zoneInfo string
			if countErr == nil {
				zoneInfo = fmt.Sprintf("%d active zones", count)
			} else {
				zoneInfo = "active zones"
			}

			errMsg := fmt.Sprintf("Cannot remove pool %s from multipool configuration: pool has %s. Please delete all zones from this pool first. Will automatically retry in 1 minute", poolName, zoneInfo)
			Log.Info(errMsg)
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.InputReadyErrorMessage,
				errMsg))
			return ctrl.Result{RequeueAfter: time.Minute}, nil
		}

		Log.Info(fmt.Sprintf("Pool %s has no active zones, removal allowed", poolName))
	}

	Log.Info(fmt.Sprintf("All removed pools (%v) validated successfully - no active zones found", removedPools))
	return ctrl.Result{}, nil
}

// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdnses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdnses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdnses/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbaccounts/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=keystone.openstack.org,resources=keystoneapis,verbs=get;list;watch
// +kubebuilder:rbac:groups=rabbitmq.openstack.org,resources=transporturls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.openstack.org,resources=redises,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;create;update;patch;delete;watch

// service account, role, rolebinding
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch
// service account permissions that are needed to grant permission to the above
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid;privileged,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get

// Reconcile -
func (r *DesignateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	Log := r.GetLogger(ctx)

	// Fetch the Designate instance
	instance := &designatev1beta1.Designate{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected.
			// For additional cleanup logic use finalizers. Return and don't requeue.
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// initialize status if Conditions is nil, but do not reset if it already
	// exists
	isNewInstance := instance.Status.Conditions == nil
	if isNewInstance {
		instance.Status.Conditions = condition.Conditions{}
	}

	// Save a copy of the condtions so that we can restore the LastTransitionTime
	// when a condition's state doesn't change.
	savedConditions := instance.Status.Conditions.DeepCopy()

	// Always patch the instance status when exiting this function so we can
	// persist any changes.
	defer func() {
		// Don't update the status, if Reconciler Panics
		if r := recover(); r != nil {
			Log.Info(fmt.Sprintf("Panic during reconcile %v\n", r))
			panic(r)
		}
		condition.RestoreLastTransitionTimes(
			&instance.Status.Conditions, savedConditions)
		if instance.Status.Conditions.IsUnknown(condition.ReadyCondition) {
			instance.Status.Conditions.Set(
				instance.Status.Conditions.Mirror(condition.ReadyCondition))
		}
		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	//
	// initialize status
	//
	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
		condition.UnknownCondition(condition.DBReadyCondition, condition.InitReason, condition.DBReadyInitMessage),
		condition.UnknownCondition(condition.DBSyncReadyCondition, condition.InitReason, condition.DBSyncReadyInitMessage),
		condition.UnknownCondition(condition.RabbitMqTransportURLReadyCondition, condition.InitReason, condition.RabbitMqTransportURLReadyInitMessage),
		condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateAPIReadyCondition, condition.InitReason, designatev1beta1.DesignateAPIReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateCentralReadyCondition, condition.InitReason, designatev1beta1.DesignateCentralReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateWorkerReadyCondition, condition.InitReason, designatev1beta1.DesignateWorkerReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateMdnsReadyCondition, condition.InitReason, designatev1beta1.DesignateMdnsReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateUnboundReadyCondition, condition.InitReason, designatev1beta1.DesignateUnboundReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateProducerReadyCondition, condition.InitReason, designatev1beta1.DesignateProducerReadyInitMessage),
		condition.UnknownCondition(designatev1beta1.DesignateBackendbind9ReadyCondition, condition.InitReason, designatev1beta1.DesignateBackendbind9ReadyInitMessage),
		// service account, role, rolebinding conditions
		condition.UnknownCondition(condition.ServiceAccountReadyCondition, condition.InitReason, condition.ServiceAccountReadyInitMessage),
		condition.UnknownCondition(condition.RoleReadyCondition, condition.InitReason, condition.RoleReadyInitMessage),
		condition.UnknownCondition(condition.RoleBindingReadyCondition, condition.InitReason, condition.RoleBindingReadyInitMessage),
	)
	instance.Status.Conditions.Init(&cl)
	instance.Status.ObservedGeneration = instance.Generation

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) || isNewInstance {
		return ctrl.Result{}, nil
	}

	if instance.Status.Hash == nil {
		instance.Status.Hash = map[string]string{}
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, instance, helper)
}

// fields to index to reconcile when change
const (
	passwordSecretField     = ".spec.secret"
	caBundleSecretNameField = ".spec.tls.caBundleSecretName" // #nosec G101
	tlsAPIInternalField     = ".spec.tls.api.internal.secretName"
	tlsAPIPublicField       = ".spec.tls.api.public.secretName"
	topologyField           = ".spec.topologyRef.Name"
)

// SetupWithManager sets up the controller with the Manager.
func (r *DesignateReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// transportURLSecretFn - Watch for changes made to the secret associated with the RabbitMQ
	// TransportURL created and used by Designate CRs.  Watch functions return a list of namespace-scoped
	// CRs that then get fed  to the reconciler.  Hence, in this case, we need to know the name of the
	// Designate CR associated with the secret we are examining in the function.  We could parse the name
	// out of the "%s-designate-transport" secret label, which would be faster than getting the list of
	// the Designate CRs and trying to match on each one.  The downside there, however, is that technically
	// someone could randomly label a secret "something-designate-transport" where "something" actually
	// matches the name of an existing Designate CR.  In that case changes to that secret would trigger
	// reconciliation for a Designate CR that does not need it.
	//
	// TODO: We also need a watch func to monitor for changes to the secret referenced by Designate.Spec.Secret
	Log := r.GetLogger(ctx)

	transportURLSecretFn := func(_ context.Context, o client.Object) []reconcile.Request {
		result := []reconcile.Request{}

		// get all Designate CRs
		designates := &designatev1beta1.DesignateList{}
		listOpts := []client.ListOption{
			client.InNamespace(o.GetNamespace()),
		}
		if err := r.List(context.Background(), designates, listOpts...); err != nil {
			Log.Error(err, "Unable to retrieve Designate CRs %v")
			return nil
		}

		for _, ownerRef := range o.GetOwnerReferences() {
			if ownerRef.Kind == "TransportURL" {
				for _, cr := range designates.Items {
					if ownerRef.Name == fmt.Sprintf("%s-designate-transport", cr.Name) {
						// return namespace and Name of CR
						name := client.ObjectKey{
							Namespace: o.GetNamespace(),
							Name:      cr.Name,
						}
						Log.Info(fmt.Sprintf("TransportURL Secret %s belongs to TransportURL belonging to Designate CR %s", o.GetName(), cr.Name))
						result = append(result, reconcile.Request{NamespacedName: name})
					}
				}
			}
		}
		if len(result) > 0 {
			return result
		}
		return nil
	}

	// TODO(beagles):
	// - Watch for changes to the redis PODs and resync the headless hostnames for the PODs if necessary.
	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1beta1.Designate{}).
		Owns(&mariadbv1.MariaDBDatabase{}).
		Owns(&mariadbv1.MariaDBAccount{}).
		Owns(&designatev1beta1.DesignateAPI{}).
		Owns(&designatev1beta1.DesignateCentral{}).
		Owns(&designatev1beta1.DesignateWorker{}).
		Owns(&designatev1beta1.DesignateMdns{}).
		Owns(&designatev1beta1.DesignateProducer{}).
		Owns(&designatev1beta1.DesignateBackendbind9{}).
		Owns(&designatev1beta1.DesignateUnbound{}).
		Owns(&corev1.Secret{}).
		Owns(&appsv1.Deployment{}).
		Owns(&rabbitmqv1.TransportURL{}).
		Owns(&redisv1.Redis{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		// Watch for multipool ConfigMap changes to regenerate pools.yaml
		Watches(&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.findDesignatesForMultipoolConfigMap)).
		// Watch for TransportURL Secrets which belong to any TransportURLs created by Designate CRs
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(transportURLSecretFn)).
		Complete(r)
}

// findDesignatesForMultipoolConfigMap watches the multipool ConfigMap and triggers
// reconciliation of all Designate CRs when it changes or is deleted
func (r *DesignateReconciler) findDesignatesForMultipoolConfigMap(ctx context.Context, obj client.Object) []reconcile.Request {
	Log := r.GetLogger(ctx)

	// Only trigger on multipool ConfigMap
	if obj.GetName() != designate.MultipoolConfigMapName {
		return nil
	}

	Log.Info("Multipool ConfigMap changed/deleted, reconciling all Designate CRs to regenerate pools.yaml")

	// get all Designate CRs in this namespace
	designates := &designatev1beta1.DesignateList{}
	listOpts := []client.ListOption{
		client.InNamespace(obj.GetNamespace()),
	}
	if err := r.List(ctx, designates, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve Designate CRs")
		return nil
	}

	result := []reconcile.Request{}
	for _, cr := range designates.Items {
		name := client.ObjectKey{
			Namespace: obj.GetNamespace(),
			Name:      cr.Name,
		}
		result = append(result, reconcile.Request{NamespacedName: name})
	}

	return result
}

func (r *DesignateReconciler) reconcileDelete(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' delete", instance.Name))

	// remove db finalizer first
	designateDb, err := mariadbv1.GetDatabaseByNameAndAccount(ctx, helper, designate.DatabaseCRName, instance.Spec.DatabaseAccount, instance.Namespace)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !k8s_errors.IsNotFound(err) {
		if err := designateDb.DeleteFinalizer(ctx, helper); err != nil {
			return ctrl.Result{}, err
		}
	}

	// TODO: We might need to control how the sub-services (API, Backup, Scheduler and Volumes) are
	// deleted (when their parent Designate CR is deleted) once we further develop their functionality

	// Service is deleted so remove the finalizer.
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	Log.Info(fmt.Sprintf("Reconciled Service '%s' delete successfully", instance.Name))

	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileInit(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' init", instance.Name))

	// ConfigMap
	configMapVars := make(map[string]env.Setter)

	designateDb, result, err := r.ensureDB(ctx, helper, instance)
	if err != nil {
		return ctrl.Result{}, err
	} else if (result != ctrl.Result{}) {
		return result, nil
	}

	//
	// create Configmap required for designate input
	// - %-scripts configmap holding scripts to e.g. bootstrap the service
	// - %-config configmap holding minimal designate config required to get the service up, user can add additional files to be added to the service
	// - parameters which has passwords gets added from the OpenStack secret via the init container
	//
	Log.Info("pre generateConfigMap ....")

	err = r.generateServiceConfigMaps(ctx, helper, instance, &configMapVars, designateDb)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	Log.Info("post generateConfigMap ....")

	//
	// create hash over all the different input resources to identify if any those changed
	// and a restart/recreate is required.
	//
	_, hashChanged, err := r.createHashOfInputHashes(ctx, instance, common.InputHashName, configMapVars, nil)
	if err != nil {
		return ctrl.Result{}, err
	} else if hashChanged {
		Log.Info("input hashes have changed")
		instance.Status.Conditions.MarkFalse(
			condition.ServiceConfigReadyCondition,
			condition.InitReason,
			condition.SeverityInfo,
			condition.ServiceConfigReadyInitMessage)
		return ctrl.Result{}, err
	}
	// Create ConfigMaps and Secrets - end

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	//
	// run Designate db sync
	//
	dbSyncHash := instance.Status.Hash[designatev1beta1.DbSyncHash]
	jobDef := designate.DbSyncJob(instance, serviceLabels, serviceAnnotations)

	Log.Info("Initializing db sync job")
	dbSyncjob := job.NewJob(
		jobDef,
		designatev1beta1.DbSyncHash,
		instance.Spec.PreserveJobs,
		time.Duration(5)*time.Second,
		dbSyncHash,
	)
	ctrlResult, err := dbSyncjob.DoJob(
		ctx,
		helper,
	)
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBSyncReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DBSyncReadyRunningMessage))
		return ctrlResult, nil
	}
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBSyncReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DBSyncReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if dbSyncjob.HasChanged() {
		instance.Status.Hash[designatev1beta1.DbSyncHash] = dbSyncjob.GetHash()
		Log.Info(fmt.Sprintf("Service '%s' - Job %s hash added - %s", instance.Name, jobDef.Name, instance.Status.Hash[designatev1beta1.DbSyncHash]))
	}
	instance.Status.Conditions.MarkTrue(condition.DBSyncReadyCondition, condition.DBSyncReadyMessage)

	// run Designate db sync - end

	Log.Info(fmt.Sprintf("Reconciled Service '%s' init successfully", instance.Name))
	return ctrl.Result{}, nil
}

// ensureDB - set up the main database, and then drives the ability to generate the config
func (r *DesignateReconciler) ensureDB(
	ctx context.Context,
	helper *helper.Helper,
	instance *designatev1beta1.Designate,
) (*mariadbv1.Database, ctrl.Result, error) {

	// ensure MariaDBAccount exists.  This account record may be created by
	// openstack-operator or the cloud operator up front without a specific
	// MariaDBDatabase configured yet.   Otherwise, a MariaDBAccount CR is
	// created here with a generated username as well as a secret with
	// generated password.   The MariaDBAccount is created without being
	// yet associated with any MariaDBDatabase.

	_, _, err := mariadbv1.EnsureMariaDBAccount(
		ctx, helper, instance.Spec.DatabaseAccount,
		instance.Namespace, false, designate.DatabaseUsernamePrefix,
	)

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			mariadbv1.MariaDBAccountReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			mariadbv1.MariaDBAccountNotReadyMessage,
			err.Error()))

		return nil, ctrl.Result{}, err
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.MariaDBAccountReadyCondition,
		mariadbv1.MariaDBAccountReadyMessage)

	//
	// create service DB instance
	//
	designateDb := mariadbv1.NewDatabaseForAccount(
		instance.Spec.DatabaseInstance, // mariadb/galera service to target
		designate.DatabaseName,         // name used in CREATE DATABASE in mariadb
		designate.DatabaseCRName,       // CR name for MariaDBDatabase
		instance.Spec.DatabaseAccount,  // CR name for MariaDBAccount
		instance.Namespace,             // namespace
	)

	// create or patch the DB
	ctrlResult, err := designateDb.CreateOrPatchAll(ctx, helper)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DBReadyErrorMessage,
			err.Error()))
		return designateDb, ctrl.Result{}, err
	}
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DBReadyRunningMessage))
		return designateDb, ctrlResult, nil
	}
	// wait for the DB to be setup
	ctrlResult, err = designateDb.WaitForDBCreated(ctx, helper)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DBReadyErrorMessage,
			err.Error()))
		return designateDb, ctrlResult, nil
	}
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DBReadyRunningMessage))
		return designateDb, ctrlResult, nil
	}

	// update Status.DatabaseHostname, used to config the service
	instance.Status.DatabaseHostname = designateDb.GetDatabaseHostname()
	instance.Status.Conditions.MarkTrue(condition.DBReadyCondition, condition.DBReadyMessage)

	return designateDb, ctrl.Result{}, nil
	// create service DB - end
}

func (r *DesignateReconciler) reconcileNormal(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s'", instance.Name))

	// Service account, role, binding
	rbacRules := []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"anyuid", "privileged"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"create", "get", "list", "watch", "update", "patch", "delete"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
	}

	//
	// create RabbitMQ transportURL CR and get the actual URL from the associated secret that is created
	//

	transportURL, op, err := r.transportURLCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.RabbitMqTransportURLReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.RabbitMqTransportURLReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		Log.Info(fmt.Sprintf("TransportURL %s successfully reconciled - operation: %s", transportURL.Name, string(op)))
	}

	instance.Status.TransportURLSecret = transportURL.Status.SecretName

	if instance.Status.TransportURLSecret == "" {
		Log.Info(fmt.Sprintf("Waiting for TransportURL %s secret to be created", transportURL.Name))
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.InputReadyWaitingMessage))
		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	instance.Status.Conditions.MarkTrue(
		condition.RabbitMqTransportURLReadyCondition,
		condition.RabbitMqTransportURLReadyMessage)

	// end transportURL
	hostIPs, err := getRedisServiceIPs(ctx, instance, helper)
	if err != nil {
		return ctrl.Result{}, err
	}

	sort.Strings(hostIPs)
	instance.Status.RedisHostIPs = hostIPs

	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	//
	// TODO check when/if Init, Update, or Upgrade should/could be skipped
	//

	serviceLabels := map[string]string{
		common.AppSelector: designate.ServiceName,
	}

	serviceAnnotations := make(map[string]string)

	// Handle service init
	ctrlResult, err := r.reconcileInit(ctx, instance, helper, serviceLabels, serviceAnnotations)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Handle service update
	ctrlResult, err = r.reconcileUpdate(ctx, instance)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Handle service upgrade
	ctrlResult, err = r.reconcileUpgrade(ctx, instance)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	//
	// normal reconcile tasks
	//
	Log.Info("Reconcile tasks starting....")

	// deploy designate-api
	designateAPI, op, err := r.apiDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateAPIReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateAPIReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	apiObsGen, err := r.checkDesignateAPIGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateAPIReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateAPIReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !apiObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateAPIReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateAPIReadyInitMessage,
		))
	} else {
		// Mirror DesignateAPI status' ReadyCount to this parent CR
		instance.Status.DesignateAPIReadyCount = designateAPI.Status.ReadyCount
		// Mirror DesignateAPI's condition status
		c := designateAPI.Status.Conditions.Mirror(designatev1beta1.DesignateAPIReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}

	if op != controllerutil.OperationResultNone && apiObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment API task reconciled")

	// Get multipool configuration once for use in bind IP allocation and pools.yaml generation
	multipoolConfig, err := r.getMultipoolConfig(ctx, helper.GetClient(), instance.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Validate pool removals - check if any removed pools have active zones
	ctrlResult, err = r.validatePoolRemovals(ctx, instance, helper, multipoolConfig)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	// Store current pool list in status hash for next reconciliation (only if changed)
	var currentPoolNames []string
	if multipoolConfig != nil {
		for _, pool := range multipoolConfig.Pools {
			currentPoolNames = append(currentPoolNames, pool.Name)
		}
	}
	newPoolList := strings.Join(currentPoolNames, ",")
	if instance.Status.Hash["multipool-pools"] != newPoolList {
		instance.Status.Hash["multipool-pools"] = newPoolList
	}

	// Handle Mdns predictable IPs configmap
	nad, err := nad.GetNADWithName(ctx, helper, instance.Spec.DesignateNetworkAttachment, instance.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	networkParameters, err := designate.GetNetworkParametersFromNAD(nad)
	if err != nil {
		return ctrl.Result{}, err
	}

	//
	// Predictable IPs.
	//
	// NOTE(oschwart): refactoring this might be nice. This could also  be
	// optimized but the data sets are small (nodes an IP ranges are less than
	// 100) so optimization might be a waste.
	//
	predictableIPParams, err := designate.GetPredictableIPAM(networkParameters)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Fetch allocated ips from Mdns and Bind config maps and store them in allocatedIPs
	mdnsLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), map[string]string{})
	mdnsConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.MdnsPredIPConfigMap, mdnsLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	bindLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), map[string]string{})
	bindConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.BindPredIPConfigMap, bindLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	nsRecordsLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), map[string]string{})
	nsRecords, err := r.getNSRecords(ctx, helper, instance, nsRecordsLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	allocatedIPs := make(map[string]bool)
	for _, predIP := range bindConfigMap.Data {
		allocatedIPs[predIP] = true
	}
	for _, predIP := range mdnsConfigMap.Data {
		allocatedIPs[predIP] = true
	}

	// Handle Mdns predictable IPs configmap
	// We cannot have 0 mDNS pods so even though the CRD validation allows 0, don't allow it.
	mdnsReplicaCount := max(int(*instance.Spec.DesignateMdns.Replicas), 1)
	var mdnsNames []string
	for i := 0; i < mdnsReplicaCount; i++ {
		mdnsNames = append(mdnsNames, fmt.Sprintf("mdns_address_%d", i))
	}

	updatedMap, allocatedIPs, err := r.allocatePredictableIPs(ctx, predictableIPParams, mdnsNames, mdnsConfigMap.Data, allocatedIPs)
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), mdnsConfigMap, func() error {
		mdnsConfigMap.Labels = util.MergeStringMaps(mdnsConfigMap.Labels, mdnsLabels)
		mdnsConfigMap.Data = updatedMap
		return controllerutil.SetControllerReference(instance, mdnsConfigMap, helper.GetScheme())
	})

	if err != nil {
		Log.Info("Unable to create config map for mdns ips...")
		return ctrl.Result{}, err
	}

	// Handle Bind predictable IPs configmap
	// Unlike mDNS, we can have 0 binds when byob is used.
	// NOTE(beagles) Really it might make more sense to have BYOB be an explicit flag and not assume that a 0
	// value is a byob case. Something to think about.
	var bindNames []string
	totalBinds := 0
	if multipoolConfig != nil {
		for _, pool := range multipoolConfig.Pools {
			totalBinds += int(pool.BindReplicas)
		}
	} else {
		totalBinds = int(*instance.Spec.DesignateBackendbind9.Replicas)
	}
	for i := range totalBinds {
		bindNames = append(bindNames, fmt.Sprintf("bind_address_%d", i))
	}

	updatedBindMap, _, err := r.allocatePredictableIPs(ctx, predictableIPParams, bindNames, bindConfigMap.Data, allocatedIPs)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Reconcile all bind IP ConfigMaps (main ConfigMap + per-pool ConfigMaps in multipool mode)
	ctrlResult, err = r.reconcileBindConfigMaps(ctx, instance, helper, multipoolConfig, updatedBindMap, bindLabels)
	if err != nil || (ctrlResult != ctrl.Result{}) {
		return ctrlResult, err
	}

	if len(nsRecords) > 0 && instance.Status.DesignateCentralReadyCount > 0 {
		Log.Info("NS records data found")
		poolsYamlConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      designate.PoolsYamlConfigMap,
				Namespace: instance.GetNamespace(),
				Labels:    bindLabels,
			},
			Data: make(map[string]string),
		}

		poolsYaml, poolsYamlHash, err := designate.GeneratePoolsYamlDataAndHash(updatedBindMap, mdnsConfigMap.Data, nsRecords, multipoolConfig)
		if err != nil {
			return ctrl.Result{}, err
		}

		Log.Info(fmt.Sprintf("pools.yaml content is\n%v", poolsYaml))
		updatedPoolsYaml := make(map[string]string)
		updatedPoolsYaml[designate.PoolsYamlContent] = poolsYaml

		_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), poolsYamlConfigMap, func() error {
			poolsYamlConfigMap.Labels = util.MergeStringMaps(poolsYamlConfigMap.Labels, bindLabels)
			poolsYamlConfigMap.Data = updatedPoolsYaml
			return controllerutil.SetControllerReference(instance, poolsYamlConfigMap, helper.GetScheme())
		})
		if err != nil {
			Log.Info("Unable to create config map for pools.yaml file")
			return ctrl.Result{}, err
		}

		if instance.Status.Hash == nil {
			instance.Status.Hash = make(map[string]string)
		}

		oldHash := instance.Status.Hash[designatev1beta1.PoolUpdateHash]
		if oldHash != poolsYamlHash {
			Log.Info(fmt.Sprintf("Old poolsYamlHash %s is different than new poolsYamlHash %s.\nLaunching pool update job", oldHash, poolsYamlHash))

			jobDef := designate.PoolUpdateJob(instance, serviceLabels, serviceAnnotations)
			Log.Info("Initializing pool update job")
			poolUpdatejob := job.NewJob(
				jobDef,
				designatev1beta1.PoolUpdateHash,
				instance.Spec.PreserveJobs,
				time.Duration(15)*time.Second,
				oldHash,
			)

			_, err := poolUpdatejob.DoJob(ctx, helper)
			if err != nil {
				return ctrl.Result{}, err
			}
			Log.Info("Pool update job completed successfully")
			instance.Status.Hash[designatev1beta1.PoolUpdateHash] = poolsYamlHash
		}
	}

	// deploy designate-central
	designateCentral, op, err := r.centralDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateCentralReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateCentralReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	ctrObsGen, err := r.checkDesignateCentralGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateCentralReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateCentralReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !ctrObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateCentralReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateCentralReadyInitMessage,
		))
	} else {
		// Mirror DesignateCentral status' ReadyCount to this parent CR
		instance.Status.DesignateCentralReadyCount = designateCentral.Status.ReadyCount
		// Mirror DesignateCentral's condition status
		c := designateCentral.Status.Conditions.Mirror(designatev1beta1.DesignateCentralReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && ctrObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	Log.Info("Deployment Central task reconciled")

	// deploy designate-worker
	designateWorker, op, err := r.workerDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateWorkerReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateWorkerReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	workerObsGen, err := r.checkDesignateWorkerGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateWorkerReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateWorkerReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !workerObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateWorkerReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateWorkerReadyInitMessage,
		))
	} else {
		// Mirror DesignateWorker status' ReadyCount to this parent CR
		instance.Status.DesignateWorkerReadyCount = designateWorker.Status.ReadyCount
		// Mirror DesignateWorker's condition status
		c := designateWorker.Status.Conditions.Mirror(designatev1beta1.DesignateWorkerReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && workerObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment Worker task reconciled")

	// deploy designate-mdns
	designateMdns, op, err := r.mdnsStatefulSetCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateMdnsReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateMdnsReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	mdnsObsGen, err := r.checkDesignateMdnsGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateMdnsReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateMdnsReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !mdnsObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateMdnsReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateMdnsReadyInitMessage,
		))
	} else {
		// Mirror DesignateMdns status' ReadyCount to this parent CR
		instance.Status.DesignateMdnsReadyCount = designateMdns.Status.ReadyCount
		// Mirror DesignateMdns's condition status
		c := designateMdns.Status.Conditions.Mirror(designatev1beta1.DesignateMdnsReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && mdnsObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment Mdns task reconciled")

	// deploy designate-producer
	designateProducer, op, err := r.producerDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateProducerReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateProducerReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	prodObsGen, err := r.checkDesignateProducerGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateProducerReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateProducerReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !prodObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateProducerReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateProducerReadyInitMessage,
		))
	} else {
		// Mirror DesignateProducer status' ReadyCount to this parent CR
		instance.Status.DesignateProducerReadyCount = designateProducer.Status.ReadyCount
		// Mirror DesignateProducer's condition status
		c := designateProducer.Status.Conditions.Mirror(designatev1beta1.DesignateProducerReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && prodObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment Producer task reconciled")

	// deploy designate-backendbind9
	designateBackendbind9, op, err := r.backendbind9StatefulSetCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateBackendbind9ReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateBackendbind9ReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	bindObsGen, err := r.checkDesignateBindGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateBackendbind9ReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateBackendbind9ReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !bindObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateBackendbind9ReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateBackendbind9ReadyInitMessage,
		))
	} else {
		// Mirror DesignateBackendbind9 status' ReadyCount to this parent CR
		instance.Status.DesignateBackendbind9ReadyCount = designateBackendbind9.Status.ReadyCount
		// Mirror DesignateBackendbind9's condition status
		c := designateBackendbind9.Status.Conditions.Mirror(designatev1beta1.DesignateBackendbind9ReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && bindObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment Backendbind9 task reconciled")

	// deploy the unbound reconcilier if necessary
	designateUnbound, op, err := r.unboundStatefulSetCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateUnboundReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateUnboundReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	unbObsGen, err := r.checkDesignateUnboundGeneration(instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateUnboundReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateUnboundReadyErrorMessage,
			err.Error()))
		return ctrlResult, nil
	}
	if !unbObsGen {
		instance.Status.Conditions.Set(condition.UnknownCondition(
			designatev1beta1.DesignateUnboundReadyCondition,
			condition.InitReason,
			designatev1beta1.DesignateUnboundReadyInitMessage,
		))
	} else {
		instance.Status.DesignateUnboundReadyCount = designateUnbound.Status.ReadyCount
		// Mirror DesignateProducer's condition status
		c := designateUnbound.Status.Conditions.Mirror(designatev1beta1.DesignateUnboundReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}
	}
	if op != controllerutil.OperationResultNone && unbObsGen {
		Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}
	Log.Info("Deployment Unbound task reconciled")

	// remove finalizers from unused MariaDBAccount records
	err = mariadbv1.DeleteUnusedMariaDBAccountFinalizers(ctx, helper, designate.DatabaseCRName, instance.Spec.DatabaseAccount, instance.Namespace)
	if err != nil {
		return ctrl.Result{}, err
	}

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}
	Log.Info("Reconciled Service successfully")
	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) getNSRecords(ctx context.Context, helper *helper.Helper, instance *designatev1beta1.Designate, labels map[string]string) ([]designatev1beta1.DesignateNSRecord, error) {
	Log := r.GetLogger(ctx)

	// First check if NS records are defined in the CR
	if len(instance.Spec.NSRecords) > 0 {
		Log.Info("Using NS records from CR", "count", len(instance.Spec.NSRecords))
		return instance.Spec.NSRecords, nil
	}

	// If NS records were not created via CR, fall back to ConfigMap
	Log.Info("No NS records were found in CR, checking ConfigMap")
	nsRecordsConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.NsRecordsConfigMap, labels)
	if err != nil {
		return nil, err
	}

	var allNSRecords []designatev1beta1.DesignateNSRecord
	for _, data := range nsRecordsConfigMap.Data {
		var nsRecords []designatev1beta1.DesignateNSRecord
		err := yaml.Unmarshal([]byte(data), &nsRecords)
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling yaml: %w", err)
		}
		allNSRecords = append(allNSRecords, nsRecords...)
	}

	return allNSRecords, nil
}

func (r *DesignateReconciler) handleConfigMap(ctx context.Context, helper *helper.Helper, instance *designatev1beta1.Designate, configMapName string, labels map[string]string) (*corev1.ConfigMap, error) {
	Log := r.GetLogger(ctx)

	nodeConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: instance.GetNamespace(),
			Labels:    labels,
		},
		Data: make(map[string]string),
	}

	// Look for existing config map and if exists, read existing data and match
	// against nodes.
	foundMap := &corev1.ConfigMap{}
	err := helper.GetClient().Get(ctx, types.NamespacedName{Name: configMapName, Namespace: instance.GetNamespace()}, foundMap)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			Log.Info(fmt.Sprintf("configmap %s doesn't exist, creating.", configMapName))
		} else {
			return nil, err
		}
	} else {
		Log.Info("Retrieved existing map, updating..")
		nodeConfigMap.Data = foundMap.Data
	}

	return nodeConfigMap, nil
}

func (r *DesignateReconciler) allocatePredictableIPs(ctx context.Context, predictableIPParams *designate.NADIpam, ipHolders []string, existingMap map[string]string, allocatedIPs map[string]bool) (map[string]string, map[string]bool, error) {
	Log := r.GetLogger(ctx)

	updatedMap := make(map[string]string)
	var predictableIPsRequired []string

	// First scan existing allocations so we can keep existing allocations.
	// Keeping track of what's required and what already exists. If a node is
	// removed from the cluster, it's IPs will not be added to the allocated
	// list and are effectively recycled.
	for _, ipHolder := range ipHolders {
		if ipValue, ok := existingMap[ipHolder]; ok {
			updatedMap[ipHolder] = ipValue
			Log.Info(fmt.Sprintf("%s has IP mapping: %s", ipHolder, ipValue))
		} else {
			predictableIPsRequired = append(predictableIPsRequired, ipHolder)
		}
	}

	// Get new IPs using the range from predictableIPParmas minus the
	// allocatedIPs captured above.
	Log.Info(fmt.Sprintf("Allocating %d predictable IPs", len(predictableIPsRequired)))
	for _, nodeName := range predictableIPsRequired {
		ipAddress, err := designate.GetNextIP(predictableIPParams, allocatedIPs)
		if err != nil {
			// An error here is really unexpected- it means either we have
			// messed up the allocatedIPs list or the range we are assuming is
			// too small for the number of mdns pod.
			return nil, nil, err
		}
		updatedMap[nodeName] = ipAddress
	}

	return updatedMap, allocatedIPs, nil
}

func (r *DesignateReconciler) reconcileUpdate(ctx context.Context, instance *designatev1beta1.Designate) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' update", instance.Name))

	// TODO: should have minor update tasks if required
	// - delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' update successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileUpgrade(ctx context.Context, instance *designatev1beta1.Designate) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' upgrade", instance.Name))

	// TODO: should have major version upgrade tasks
	// -delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' upgrade successfully", instance.Name))
	return ctrl.Result{}, nil
}

// generateServiceConfigMaps - create secrets which hold scripts and service configuration
func (r *DesignateReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.Designate,
	envVars *map[string]env.Setter,
	designateDb *mariadbv1.Database,
) error {
	//
	// create Configmap/Secret required for designate input
	// - %-scripts configmap holding scripts to e.g. bootstrap the service
	// - %-config configmap holding minimal designate config required to get the service up, user can add additional files to be added to the service
	// - parameters which has passwords gets added from the ospSecret via the init container
	//
	Log := r.GetLogger(ctx)

	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(designate.ServiceName), map[string]string{})

	var replicas int
	multipoolConfig, err := r.getMultipoolConfig(ctx, h.GetClient(), instance.Namespace)
	if err != nil {
		return err
	}

	if multipoolConfig != nil {
		for _, pool := range multipoolConfig.Pools {
			replicas += int(pool.BindReplicas)
		}
	} else {
		replicas = int(*instance.Spec.DesignateBackendbind9.Replicas)
	}

	// Get the secret first by providing the same name and namespace
	secret := &corev1.Secret{}
	err = h.GetClient().Get(ctx, types.NamespacedName{
		Name:      designate.DesignateBindKeySecret,
		Namespace: instance.Namespace,
	}, secret)

	// Define the secret if it is not found
	if err != nil && !k8s_errors.IsNotFound(err) {
		Log.Error(err, "Failed to fetch secret")
		return err
	} else if k8s_errors.IsNotFound(err) {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      designate.DesignateBindKeySecret,
				Namespace: instance.Namespace,
			},
		}
	}

	// Update the secret if it exists, instantiate it otherwise
	_, err = controllerutil.CreateOrUpdate(ctx, h.GetClient(), secret, func() error {
		secret.Labels = cmLabels
		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		newKeysMap := make(map[string][]byte)

		for i := range replicas {
			keyName := fmt.Sprintf("%s-%v", designate.DesignateRndcKey, i)

			if key, exists := secret.Data[keyName]; exists {
				newKeysMap[keyName] = key
				Log.Info(fmt.Sprintf("key %s existed and therefore was not added", keyName))
			} else {
				// If key doesn't exist, generate a new one
				rndcKeyContent, err := designate.CreateRndcKeySecret()
				if err != nil {
					return err
				}
				newKeysMap[keyName] = []byte(rndcKeyContent)
				Log.Info(fmt.Sprintf("key %s did not exist, was created and added", keyName))
			}
		}
		secret.Data = newKeysMap
		return nil
	})

	if err != nil {
		Log.Error(err, "Failed to create or update secret")
		return err
	}

	// TLS handling
	var tlsCfg *tls.Service
	if instance.Spec.DesignateAPI.TLS.CaBundleSecretName != "" {
		tlsCfg = &tls.Service{}
	}

	// customData hold any customization for the service.
	// custom.conf is going to /etc/<service>/<service>.conf.d
	// all other files get placed into /etc/<service> to allow overwrite of e.g. policy.json
	// TODO: make sure custom.conf can not be overwritten
	customData := map[string]string{
		common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig,
		"my.cnf":                           designateDb.GetDatabaseClientConfig(tlsCfg), //(oschwart) for now just get the default my.cnf
	}

	databaseAccount := designateDb.GetAccount()
	dbSecret := designateDb.GetSecret()

	// We only need a minimal 00-config.conf that is only used by db-sync job,
	// hence only passing the database related parameters
	templateParameters := map[string]any{
		"MinimalConfig": true, // This tells the template to generate a minimal config
		"DatabaseConnection": fmt.Sprintf("mysql+pymysql://%s:%s@%s/%s?read_default_file=/etc/my.cnf",
			databaseAccount.Spec.UserName,
			string(dbSecret.Data[mariadbv1.DatabasePasswordSelector]),
			instance.Status.DatabaseHostname,
			designate.DatabaseName,
		),
	}

	transportURLSecret, _, err := oko_secret.GetSecret(ctx, h, instance.Status.TransportURLSecret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Since the TransportURL secret should have been automatically created by the operator,
			// but if reconciliation reaches this point and the secret is somehow missing, we treat
			// this as a warning because ithat the service will not be able to start.
			Log.Info(fmt.Sprintf("TransportURL secret %s not found", instance.Status.TransportURLSecret))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.InputReadyWaitingMessage))
			return nil
		}
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return err
	}
	templateParameters["TransportURL"] = string(transportURLSecret.Data["transport_url"])
	templateParameters["QuorumQueues"] = string(transportURLSecret.Data["quorumqueues"]) == "true"

	adminPasswordSecret, _, err := oko_secret.GetSecret(ctx, h, instance.Spec.Secret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Since the service secret should have been manually created by the user and referenced in the spec,
			// we treat this as a warning because it means that the service will not be able to start.
			Log.Info(fmt.Sprintf("AdminPassword secret %s not found", instance.Spec.Secret))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.InputReadyWaitingMessage))
			return nil
		}
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return err
	}
	templateParameters["AdminPassword"] = string(adminPasswordSecret.Data["DesignatePassword"])

	redisIPs, err := getRedisServiceIPs(ctx, instance, h)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return err
	}

	if len(redisIPs) == 0 {
		err = designate.ErrRedisRequired
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return err
	}

	sort.Strings(redisIPs)

	// TODO(beagles): This should be set to sentinel services! There seems to be a problem with sentinels at them moment.
	// We should also check for IPv6 validity.
	backendURL := fmt.Sprintf("redis://%s:6379/", redisIPs[0])
	if tlsCfg != nil {
		backendURL = fmt.Sprintf("%s?ssl=true", backendURL)
	}
	templateParameters["CoordinationBackendURL"] = backendURL

	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:         designate.ScriptsVolumeName(instance.Name),
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeScripts,
			InstanceType: instance.Kind,
			AdditionalTemplate: map[string]string{
				"common.sh": "/common/common.sh",
				"init.sh":   "/common/init.sh",
			},
			Labels: cmLabels,
		},
		// This will be the common config for all OpenStack based services. This will be merged with
		// service specific configurations.
		{
			Name:          designate.ConfigVolumeName(instance.Name),
			Namespace:     instance.Namespace,
			Type:          util.TemplateTypeConfig,
			InstanceType:  instance.Kind,
			CustomData:    customData,
			ConfigOptions: templateParameters,
			Labels:        cmLabels,
		},
		{
			Name:         designate.DefaultsVolumeName(instance.Name),
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeNone,
			InstanceType: instance.Kind,
			CustomData:   instance.Spec.DefaultConfigOverwrite,
			Labels:       cmLabels,
		},
	}

	err = oko_secret.EnsureSecrets(ctx, h, instance, cms, envVars)
	if err != nil {
		return err
	}

	return nil
}

// createHashOfInputHashes - creates a hash of hashes which gets added to the resources which requires a restart
// if any of the input resources change, like configs, passwords, ...
//
// returns the hash, whether the hash changed (as a bool) and any error
func (r *DesignateReconciler) createHashOfInputHashes(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	hashType string,
	envVars map[string]env.Setter,
	additionalConfigmaps []any,
) (string, bool, error) {
	Log := r.GetLogger(ctx)

	var hashMap map[string]string
	changed := false
	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	combinedHashes := []string{}

	envHash, err := util.ObjectHash(mergedMapVars)
	if err != nil {
		Log.Info("Error creating hash")
		return "", changed, err
	}
	combinedHashes = append(combinedHashes, envHash)

	for _, configMap := range additionalConfigmaps {
		configMapHash, err := util.ObjectHash(configMap)
		if err != nil {
			Log.Info(fmt.Sprintf("Error creating hash for %v", configMap))
			return "", changed, err
		}
		combinedHashes = append(combinedHashes, configMapHash)
	}

	finalHash, err := util.ObjectHash(combinedHashes)
	if err != nil {
		Log.Info("Error creating final hash")
		return "", changed, err
	}

	if hashMap, changed = util.SetHash(instance.Status.Hash, hashType, finalHash); changed {
		instance.Status.Hash = hashMap
		Log.Info(fmt.Sprintf("Input maps hash %s - %s", hashType, finalHash))
	}

	return finalHash, changed, nil
}

func (r *DesignateReconciler) transportURLCreateOrUpdate(
	ctx context.Context,
	instance *designatev1beta1.Designate,
) (*rabbitmqv1.TransportURL, controllerutil.OperationResult, error) {
	transportURL := &rabbitmqv1.TransportURL{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-designate-transport", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, transportURL, func() error {
		transportURL.Spec.RabbitmqClusterName = instance.Spec.RabbitMqClusterName

		err := controllerutil.SetControllerReference(instance, transportURL, r.Scheme)
		return err
	})

	return transportURL, op, err
}

// copyDesignateTemplateItems - copy elements from the central Spec to the sub-spec template.
func copyDesignateTemplateItems(src *designatev1beta1.DesignateSpecBase, dest *designatev1beta1.DesignateTemplate) {
	dest.ServiceUser = getOrDefault(src.ServiceUser, "designate")
	dest.DatabaseAccount = getOrDefault(src.DatabaseAccount, "designate")
	dest.Secret = src.Secret
	dest.PasswordSelectors.Service = getOrDefault(src.PasswordSelectors.Service, "DesignatePassword")
}

func (r *DesignateReconciler) apiDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateAPI, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateAPI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-api", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateAPI.NodeSelector == nil {
		instance.Spec.DesignateAPI.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateAPI.TopologyRef == nil {
		instance.Spec.DesignateAPI.TopologyRef = instance.Spec.TopologyRef
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateAPI
		// Add in transfers from umbrella Designate (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		copyDesignateTemplateItems(&instance.Spec.DesignateSpecBase, &deployment.Spec.DesignateTemplate)
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.NodeSelector = instance.Spec.DesignateAPI.NodeSelector
		deployment.Spec.TopologyRef = instance.Spec.DesignateAPI.TopologyRef
		deployment.Spec.APITimeout = instance.Spec.APITimeout

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) centralDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateCentral, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateCentral{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-central", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateCentral.NodeSelector == nil {
		instance.Spec.DesignateCentral.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateCentral.TopologyRef == nil {
		instance.Spec.DesignateCentral.TopologyRef = instance.Spec.TopologyRef
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateCentral
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		copyDesignateTemplateItems(&instance.Spec.DesignateSpecBase, &deployment.Spec.DesignateTemplate)
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		// TODO(beagles): redundant but we cannot remove them from the API for the time being so let's keep this here.
		deployment.Spec.RedisHostIPs = instance.Status.RedisHostIPs
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateCentral.NodeSelector
		deployment.Spec.TopologyRef = instance.Spec.DesignateCentral.TopologyRef

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) workerDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateWorker, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateWorker{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-worker", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateWorker.NodeSelector == nil {
		instance.Spec.DesignateWorker.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateWorker.TopologyRef == nil {
		instance.Spec.DesignateWorker.TopologyRef = instance.Spec.TopologyRef
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateWorker
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		copyDesignateTemplateItems(&instance.Spec.DesignateSpecBase, &deployment.Spec.DesignateTemplate)
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateWorker.NodeSelector
		deployment.Spec.TopologyRef = instance.Spec.DesignateWorker.TopologyRef

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) mdnsStatefulSetCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateMdns, controllerutil.OperationResult, error) {
	statefulSet := &designatev1beta1.DesignateMdns{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-mdns", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateMdns.NodeSelector == nil {
		instance.Spec.DesignateMdns.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateMdns.TopologyRef == nil {
		instance.Spec.DesignateMdns.TopologyRef = instance.Spec.TopologyRef
	}

	if int(*instance.Spec.DesignateMdns.Replicas) < 1 {
		var minReplicas int32 = 1
		instance.Spec.DesignateMdns.Replicas = &minReplicas
	}

	if len(instance.Spec.DesignateMdns.ControlNetworkName) == 0 {
		instance.Spec.DesignateMdns.ControlNetworkName = getOrDefault(instance.Spec.DesignateNetworkAttachment, "designate")
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, statefulSet, func() error {
		statefulSet.Spec = instance.Spec.DesignateMdns
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		copyDesignateTemplateItems(&instance.Spec.DesignateSpecBase, &statefulSet.Spec.DesignateTemplate)
		statefulSet.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		statefulSet.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		statefulSet.Spec.ServiceAccount = instance.RbacResourceName()
		statefulSet.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		statefulSet.Spec.NodeSelector = instance.Spec.DesignateMdns.NodeSelector
		statefulSet.Spec.TopologyRef = instance.Spec.DesignateMdns.TopologyRef
		statefulSet.Spec.ControlNetworkName = instance.Spec.DesignateMdns.ControlNetworkName

		err := controllerutil.SetControllerReference(instance, statefulSet, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return statefulSet, op, err
}

func (r *DesignateReconciler) producerDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateProducer, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateProducer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-producer", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateProducer.NodeSelector == nil {
		instance.Spec.DesignateProducer.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateProducer.TopologyRef == nil {
		instance.Spec.DesignateProducer.TopologyRef = instance.Spec.TopologyRef
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateProducer
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		copyDesignateTemplateItems(&instance.Spec.DesignateSpecBase, &deployment.Spec.DesignateTemplate)
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.RedisHostIPs = instance.Status.RedisHostIPs
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateProducer.NodeSelector
		deployment.Spec.TopologyRef = instance.Spec.DesignateProducer.TopologyRef

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) backendbind9StatefulSetCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateBackendbind9, controllerutil.OperationResult, error) {
	statefulSet := &designatev1beta1.DesignateBackendbind9{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-backendbind9", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateBackendbind9.NodeSelector == nil {
		instance.Spec.DesignateBackendbind9.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateBackendbind9.TopologyRef == nil {
		instance.Spec.DesignateBackendbind9.TopologyRef = instance.Spec.TopologyRef
	}

	if len(instance.Spec.DesignateBackendbind9.ControlNetworkName) == 0 {
		instance.Spec.DesignateBackendbind9.ControlNetworkName = getOrDefault(instance.Spec.DesignateNetworkAttachment, "designate")
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, statefulSet, func() error {
		statefulSet.Spec = instance.Spec.DesignateBackendbind9
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		statefulSet.Spec.ServiceUser = instance.Spec.ServiceUser
		statefulSet.Spec.Secret = instance.Spec.Secret
		statefulSet.Spec.ServiceAccount = instance.RbacResourceName()
		statefulSet.Spec.NodeSelector = instance.Spec.DesignateBackendbind9.NodeSelector
		statefulSet.Spec.TopologyRef = instance.Spec.DesignateBackendbind9.TopologyRef
		statefulSet.Spec.ControlNetworkName = instance.Spec.DesignateBackendbind9.ControlNetworkName

		networkAttachment := "designate"
		if instance.Spec.DesignateNetworkAttachment != "" {
			networkAttachment = instance.Spec.DesignateNetworkAttachment
		}
		statefulSet.Spec.ControlNetworkName = getOrDefault(instance.Spec.DesignateBackendbind9.ControlNetworkName, networkAttachment)

		err := controllerutil.SetControllerReference(instance, statefulSet, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return statefulSet, op, err
}

func (r *DesignateReconciler) unboundStatefulSetCreateOrUpdate(
	ctx context.Context,
	instance *designatev1beta1.Designate,
) (*designatev1beta1.DesignateUnbound, controllerutil.OperationResult, error) {
	statefulSet := &designatev1beta1.DesignateUnbound{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-unbound", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateUnbound.NodeSelector == nil {
		instance.Spec.DesignateUnbound.NodeSelector = instance.Spec.NodeSelector
	}

	// If topology is not present in the underlying Service template,
	// inherit from the top-level CR
	if instance.Spec.DesignateUnbound.TopologyRef == nil {
		instance.Spec.DesignateUnbound.TopologyRef = instance.Spec.TopologyRef
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, statefulSet, func() error {
		statefulSet.Spec = instance.Spec.DesignateUnbound
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		statefulSet.Spec.ServiceAccount = instance.RbacResourceName()
		statefulSet.Spec.NodeSelector = instance.Spec.DesignateUnbound.NodeSelector
		statefulSet.Spec.TopologyRef = instance.Spec.DesignateUnbound.TopologyRef

		err := controllerutil.SetControllerReference(instance, statefulSet, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return statefulSet, op, err
}

// checkDesignateAPIGeneration -
func (r *DesignateReconciler) checkDesignateAPIGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	api := &designatev1beta1.DesignateAPIList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), api, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateAPI %w")
		return false, err
	}
	for _, item := range api.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateCentralGeneration -
func (r *DesignateReconciler) checkDesignateCentralGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	central := &designatev1beta1.DesignateCentralList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), central, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateCentral %w")
		return false, err
	}
	for _, item := range central.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateWorkerGeneration -
func (r *DesignateReconciler) checkDesignateWorkerGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	worker := &designatev1beta1.DesignateWorkerList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), worker, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateWorker %w")
		return false, err
	}
	for _, item := range worker.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateMdnsGeneration -
func (r *DesignateReconciler) checkDesignateMdnsGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	mdns := &designatev1beta1.DesignateMdnsList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), mdns, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateWorker %w")
		return false, err
	}
	for _, item := range mdns.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateProducerGeneration -
func (r *DesignateReconciler) checkDesignateProducerGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	prd := &designatev1beta1.DesignateProducerList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), prd, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateProducer %w")
		return false, err
	}
	for _, item := range prd.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateBindGeneration -
func (r *DesignateReconciler) checkDesignateBindGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	prd := &designatev1beta1.DesignateBackendbind9List{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), prd, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateBind %w")
		return false, err
	}
	for _, item := range prd.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

// checkDesignateUnboundGeneration -
func (r *DesignateReconciler) checkDesignateUnboundGeneration(
	instance *designatev1beta1.Designate,
) (bool, error) {
	Log := r.GetLogger(context.Background())
	prd := &designatev1beta1.DesignateUnboundList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
	}
	if err := r.List(context.Background(), prd, listOpts...); err != nil {
		Log.Error(err, "Unable to retrieve DesignateUnbound %w")
		return false, err
	}
	for _, item := range prd.Items {
		if item.Generation != item.Status.ObservedGeneration {
			return false, nil
		}
	}
	return true, nil
}

func getRedisServiceIPs(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
) ([]string, error) {
	getOptions := metav1.GetOptions{}
	service, err := helper.GetKClient().CoreV1().Services(instance.Namespace).Get(ctx, instance.Spec.RedisServiceName, getOptions)
	if err != nil {
		return []string{}, err
	}
	// TODO: Ensure that the correct port is exposed
	return service.Spec.ClusterIPs, nil
}
