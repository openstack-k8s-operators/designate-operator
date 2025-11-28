/*v1
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
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
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	"gopkg.in/yaml.v2"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	designatebackendbind9 "github.com/openstack-k8s-operators/designate-operator/internal/designatebackendbind9"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

// GetClient -
func (r *DesignateBackendbind9Reconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *DesignateBackendbind9Reconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetLogger returns a logger object with a prefix of "controller.name" and additional controller context fields
func (r *DesignateBackendbind9Reconciler) GetLogger(ctx context.Context) logr.Logger {
	return log.FromContext(ctx).WithName("Controllers").WithName("DesignateBackendbind9")
}

// GetScheme -
func (r *DesignateBackendbind9Reconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// DesignateBackendbind9Reconciler reconciles a DesignateBackendbind9 object
type DesignateBackendbind9Reconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

//+kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designatebackendbind9s/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
// +kubebuilder:rbac:groups=topology.openstack.org,resources=topologies,verbs=get;list;watch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DesignateBackendbind9 object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
func (r *DesignateBackendbind9Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	Log := r.GetLogger(ctx)

	// Fetch the DesignateBackendbind9 instance
	instance := &designatev1beta1.DesignateBackendbind9{}
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
		if rc := recover(); rc != nil {
			Log.Info(fmt.Sprintf("Panic during reconcile %v\n", rc))
			panic(rc)
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
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
		condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
	)

	instance.Status.Conditions.Init(&cl)
	instance.Status.ObservedGeneration = instance.Generation

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		return ctrl.Result{}, nil
	}

	if instance.Status.Hash == nil {
		instance.Status.Hash = map[string]string{}
	}
	if instance.Status.NetworkAttachments == nil {
		instance.Status.NetworkAttachments = map[string][]string{}
	}

	// Init Topology condition if there's a reference
	if instance.Spec.TopologyRef != nil {
		c := condition.UnknownCondition(condition.TopologyReadyCondition, condition.InitReason, condition.TopologyReadyInitMessage)
		cl.Set(c)
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DesignateBackendbind9Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	Log := r.GetLogger(ctx)
	// Watch for changes to any CustomServiceConfigSecrets. Global secrets
	// (e.g. TransportURLSecret) are handled by the top designate controller.
	// svcSecretFn := func(_ context.Context, o client.Object) []reconcile.Request {
	// 	var namespace string = o.GetNamespace()
	// 	var secretName string = o.GetName()
	// 	result := []reconcile.Request{}

	// 	// get all Backendbind9 CRs
	// 	apis := &designatev1beta1.DesignateBackendbind9List{}
	// 	listOpts := []client.ListOption{
	// 		client.InNamespace(namespace),
	// 	}
	// 	if err := r.Client.List(context.Background(), apis, listOpts...); err != nil {
	// 		Log.Error(err, "Unable to retrieve Backendbind9 CRs %v")
	// 		return nil
	// 	}
	// 	for _, cr := range apis.Items {
	// 		for _, v := range cr.Spec.CustomServiceConfigSecrets {
	// 			if v == secretName {
	// 				name := client.ObjectKey{
	// 					Namespace: namespace,
	// 					Name:      cr.Name,
	// 				}
	// 				Log.Info(fmt.Sprintf("Secret %s is used by Designate CR %s", secretName, cr.Name))
	// 				result = append(result, reconcile.Request{NamespacedName: name})
	// 			}
	// 		}
	// 	}
	// 	if len(result) > 0 {
	// 		return result
	// 	}
	// 	return nil
	// }

	// watch for configmap where the CM owner label AND the CR.Spec.ManagingCrName label matches
	configMapFn := func(_ context.Context, o client.Object) []reconcile.Request {
		result := []reconcile.Request{}

		// get all Backendbind9 CRs
		apis := &designatev1beta1.DesignateBackendbind9List{}
		listOpts := []client.ListOption{
			client.InNamespace(o.GetNamespace()),
		}
		if err := r.List(context.Background(), apis, listOpts...); err != nil {
			Log.Error(err, "Unable to retrieve Backendbind9 CRs %v")
			return nil
		}

		// reconcile all DesignateBackendbind9 CRs on multipool configmap change
		if o.GetName() == designate.MultipoolConfigMapName {
			Log.Info("Multipool ConfigMap changed, reconciling all DesignateBackendbind9 CRs")
			for _, cr := range apis.Items {
				name := client.ObjectKey{
					Namespace: o.GetNamespace(),
					Name:      cr.Name,
				}
				result = append(result, reconcile.Request{NamespacedName: name})
			}
			return result
		}

		label := o.GetLabels()
		// TODO: Just trying to verify that the CM is owned by this CR's managing CR
		if l, ok := label[labels.GetOwnerNameLabelSelector(labels.GetGroupLabel(designate.ServiceName))]; ok {
			for _, cr := range apis.Items {
				// return reconcil event for the CR where the CM owner label AND
				// the parentDesignateName matches
				if l == designate.GetOwningDesignateName(&cr) {
					// return namespace and Name of CR
					name := client.ObjectKey{
						Namespace: o.GetNamespace(),
						Name:      cr.Name,
					}
					Log.Info(fmt.Sprintf("ConfigMap object %s and CR %s marked with label: %s", o.GetName(), cr.Name, l))
					result = append(result, reconcile.Request{NamespacedName: name})
				}
			}
		}
		if len(result) > 0 {
			return result
		}
		return nil
	}

	// index topologyField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &designatev1beta1.DesignateBackendbind9{}, topologyField, func(rawObj client.Object) []string {
		// Extract the topology name from the spec, if one is provided
		cr := rawObj.(*designatev1beta1.DesignateBackendbind9)
		if cr.Spec.TopologyRef == nil {
			return nil
		}
		return []string{cr.Spec.TopologyRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1beta1.DesignateBackendbind9{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		// watch the config CMs we don't own
		Watches(&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(configMapFn)).
		Watches(&topologyv1.Topology{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *DesignateBackendbind9Reconciler) findObjectsForSrc(ctx context.Context, src client.Object) []reconcile.Request {
	requests := []reconcile.Request{}

	Log := r.GetLogger(ctx)

	allWatchFields := []string{
		topologyField,
	}

	for _, field := range allWatchFields {
		crList := &designatev1beta1.DesignateBackendbind9List{}
		listOps := &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(field, src.GetName()),
			Namespace:     src.GetNamespace(),
		}
		err := r.List(context.TODO(), crList, listOps)
		if err != nil {
			Log.Error(err, fmt.Sprintf("listing %s for field: %s - %s", crList.GroupVersionKind().Kind, field, src.GetNamespace()))
			return requests
		}

		for _, item := range crList.Items {
			Log.Info(fmt.Sprintf("input source %s changed, reconcile: %s - %s", src.GetName(), item.GetName(), item.GetNamespace()))

			requests = append(requests,
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      item.GetName(),
						Namespace: item.GetNamespace(),
					},
				},
			)
		}
	}
	return requests
}

func (r *DesignateBackendbind9Reconciler) reconcileDelete(ctx context.Context, instance *designatev1beta1.DesignateBackendbind9, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info(fmt.Sprintf("Reconciling Service '%s' delete", instance.Name))

	// Remove finalizer on the Topology CR
	if ctrlResult, err := topologyv1.EnsureDeletedTopologyRef(
		ctx,
		helper,
		instance.Status.LastAppliedTopology,
		instance.Name,
	); err != nil {
		return ctrlResult, err
	}
	// We did all the cleanup on the objects we created so we can remove the
	// finalizer from ourselves to allow the deletion
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	Log.Info(fmt.Sprintf("Reconciled Service '%s' delete successfully", instance.Name))

	return ctrl.Result{}, nil
}

func (r *DesignateBackendbind9Reconciler) reconcileInit(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info(fmt.Sprintf("Reconciling Service '%s' init", instance.Name))

	Log.Info(fmt.Sprintf("Reconciled Service '%s' init successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateBackendbind9Reconciler) reconcileNormal(ctx context.Context, instance *designatev1beta1.DesignateBackendbind9, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info("Reconciling Service")

	// ConfigMap
	configMapVars := make(map[string]env.Setter)

	//
	// TODO(beagles 03/2025): this block of code repeats throughout designate. It appears that the idea here was to allow custom
	// config snippets to be stored in secrets and used later on. This was not used anywhere however. Maybe this
	// was intended to be part of supporting alternate backends? I will comment out for now and add a warning if the
	// parameter is used.
	//
	// for _, secretName := range instance.Spec.CustomServiceConfigSecrets {
	//	ctrlResult, err = r.getSecret(ctx, helper, instance, secretName, &configMapVars, "secret-")
	//	if err != nil {
	//		return ctrlResult, err
	//	}
	//}
	// run check service secrets - end
	if len(instance.Spec.CustomServiceConfigSecrets) > 0 {
		Log.Info("warning: CustomServiceConfigSecrets is not supported.")
	}

	//
	// check for required Designate config maps that should have been created by parent Designate CR
	//

	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	serviceLabels := map[string]string{
		common.AppSelector:       instance.Name,
		common.ComponentSelector: designatebackendbind9.Component,
	}
	//
	// Create ConfigMaps required as input for the Service and calculate an overall hash of hashes
	//

	//
	// create custom Configmap for this designate volume service
	//
	err := r.generateServiceConfigMaps(ctx, helper, instance, &configMapVars, serviceLabels)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	// Create ConfigMaps - end

	//
	// create hash over all the different input resources to identify if any those changed
	// and a restart/recreate is required.
	//
	inputHash, hashChanged, err := r.createHashOfInputHashes(ctx, instance, configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	} else if hashChanged {
		// Hash changed and instance status should be updated (which will be done by main defer func),
		// so we need to return and reconcile again
		return ctrl.Result{}, nil
	}

	bindIPsUpdated, err := r.hasMapChanged(ctx, helper, instance, designate.BindPredIPConfigMap, designate.BindPredictableIPHash)
	if err != nil {
		return ctrl.Result{}, err
	}
	rndcUpdate, err := r.hasSecretChanged(ctx, helper, instance, designate.DesignateBindKeySecret, designate.RndcHash)
	if err != nil {
		return ctrl.Result{}, err
	}
	if rndcUpdate || bindIPsUpdated {
		// Predictable IPs and/or rndc keys have been updated, we need to update the statefulset.
		return ctrl.Result{}, nil
	}

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	// Create ConfigMaps and Secrets - end

	//
	// TODO check when/if Init, Update, or Upgrade should/could be skipped
	//
	// networks to attach to
	nadList := []networkv1.NetworkAttachmentDefinition{}
	for _, netAtt := range instance.Spec.NetworkAttachments {
		nad, err := nad.GetNADWithName(ctx, helper, netAtt, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				// Since the net-attach-def CR should have been manually created by the user and referenced in the spec,
				// we treat this as a warning because it means that the service will not be able to start.
				Log.Info(fmt.Sprintf("network-attachment-definition %s not found", netAtt))
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning,
					condition.NetworkAttachmentsReadyWaitingMessage,
					netAtt))
				return ctrl.Result{RequeueAfter: time.Second * 10}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.NetworkAttachmentsReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.NetworkAttachmentsReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		if nad != nil {
			nadList = append(nadList, *nad)
		}
	}

	serviceAnnotations, err := nad.EnsureNetworksAnnotation(nadList)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed create network annotation from %s: %w",
			instance.Spec.NetworkAttachments, err)
	}

	// Handle service init
	ctrlResult, err := r.reconcileInit(ctx, instance)
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
	// Handle Topology
	//
	topology, err := ensureTopology(
		ctx,
		helper,
		instance,      // topologyHandler
		instance.Name, // finalizer
		&instance.Status.Conditions,
		labels.GetLabelSelector(serviceLabels),
	)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.TopologyReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.TopologyReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("waiting for Topology requirements: %w", err)
	}

	//
	// normal reconcile tasks
	//

	multipoolConfig, err := designate.GetMultipoolConfig(ctx, helper.GetClient(), instance.Namespace)
	if err != nil {
		Log.Error(err, "Failed to get multipool configuration")
		return ctrl.Result{}, err
	}

	if multipoolConfig != nil {
		// Delete old single-pool services (migration from single to multipool)
		// Single-pool services follow pattern: designate-backendbind9-0, designate-backendbind9-1
		// Multipool services follow pattern: designate-backendbind9-pool0-0, designate-backendbind9-pool1-0
		svcList := &corev1.ServiceList{}
		labelSelector := map[string]string{
			"service":   "designate-backendbind9",
			"component": "designate-backendbind9",
		}
		err = helper.GetClient().List(ctx, svcList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		for _, svc := range svcList.Items {
			// Delete services without "-pool" in name (old single-pool services)
			if !strings.Contains(svc.Name, "-pool") {
				Log.Info(fmt.Sprintf("Deleting old single-pool service %s for multipool migration", svc.Name))
				err = helper.GetClient().Delete(ctx, &svc)
				if err != nil && !k8s_errors.IsNotFound(err) {
					Log.Error(err, fmt.Sprintf("Failed to delete old service %s", svc.Name))
					return ctrl.Result{}, err
				}
			}
		}

		// No need to delete the old single StatefulSet during migration to multipool
		// Pool 0 will reuse the same StatefulSet name (instance.Name) for backwards compatibility
		// This avoids unnecessary downtime and resource recreation

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

	} else {
		// Delete any multipool services if they exist (migration from multipool to single / default pool)
		svcList := &corev1.ServiceList{}
		labelSelector := map[string]string{
			"service":   "designate-backendbind9",
			"component": "designate-backendbind9",
		}
		err = helper.GetClient().List(ctx, svcList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
		if err == nil {
			for _, svc := range svcList.Items {
				// Delete pool-specific services (names contain "-pool")
				if strings.Contains(svc.Name, "-pool") {
					Log.Info(fmt.Sprintf("Deleting multipool service %s for single pool migration", svc.Name))
					err = helper.GetClient().Delete(ctx, &svc)
					if err != nil && !k8s_errors.IsNotFound(err) {
						Log.Error(err, fmt.Sprintf("Failed to delete multipool service %s", svc.Name))
						return ctrl.Result{}, err
					}
				}
			}
		} else if !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		// Delete numbered pool StatefulSets if they exist (pool1, pool2, etc.)
		// Pool0 (instance.Name) will be reused for single-pool mode
		stsList := &appsv1.StatefulSetList{}
		err = helper.GetClient().List(ctx, stsList, client.InNamespace(instance.Namespace), client.MatchingLabels(labelSelector))
		if err == nil {
			for _, sts := range stsList.Items {
				// Delete numbered pool StatefulSets (pool1, pool2, etc.)
				// Keep instance.Name (pool0) as it will be reused for single-pool mode
				if _, hasPoolLabel := sts.Labels["pool"]; hasPoolLabel && sts.Name != instance.Name {
					Log.Info(fmt.Sprintf("Deleting numbered pool StatefulSet %s for single pool migration", sts.Name))
					err = helper.GetClient().Delete(ctx, &sts)
					if err != nil && !k8s_errors.IsNotFound(err) {
						Log.Error(err, fmt.Sprintf("Failed to delete multipool StatefulSet %s", sts.Name))
						return ctrl.Result{}, err
					}
				}
			}
		} else if !k8s_errors.IsNotFound(err) {
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

		// Create single-pool services
		// TODO(oschwart): Determine if we should error out when Override.Services has fewer entries than replicas.
		// Currently using min() allows deployment to proceed with partial services (some pods have no external service),
		// which may result in a broken DNS configuration. Multipool mode (reconcileMultipoolServices) errors out in this
		// case for consistency. However, changing this behavior could break existing deployments that rely on the lenient
		// behavior. Consider: (1) error out for consistency with multipool, or (2) keep min() for backward compatibility
		// but document that all replicas need corresponding Override.Services entries for proper DNS functionality.
		serviceCount := min(int(*instance.Spec.Replicas), len(instance.Spec.Override.Services))
		for i := 0; i < serviceCount; i++ {
			svc, err := designate.CreateDNSService(
				fmt.Sprintf("designate-backendbind9-%d", i),
				instance.Namespace,
				&instance.Spec.Override.Services[i],
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

		ctrlResult, err := r.reconcileSingleStatefulSet(ctx, instance, helper, inputHash, serviceLabels, serviceAnnotations, topology)
		if err != nil || (ctrlResult != ctrl.Result{}) {
			return ctrlResult, err
		}
	}
	// create StatefulSet(s) - end

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}
	Log.Info("Reconciled Service successfully")
	return ctrl.Result{}, nil
}

func (r *DesignateBackendbind9Reconciler) reconcileSingleStatefulSet(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	helper *helper.Helper,
	inputHash string,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
	topology *topologyv1.Topology,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	// Define a new StatefulSet object
	// Use default bind IP ConfigMap for single-pool mode
	deplDef, err := designatebackendbind9.StatefulSet(instance, inputHash, serviceLabels, serviceAnnotations, topology, instance.Name, designate.BindPredIPConfigMap)
	if err != nil {
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
		return ctrlResult, nil
	}
	deploy := depl.GetStatefulSet()
	if deploy.Generation == deploy.Status.ObservedGeneration {
		instance.Status.ReadyCount = deploy.Status.ReadyReplicas

		// verify if network attachment matches expectations
		networkReady := false
		networkAttachmentStatus := map[string][]string{}
		if *(instance.Spec.Replicas) > 0 {
			networkReady, networkAttachmentStatus, err = nad.VerifyNetworkStatusFromAnnotation(
				ctx,
				helper,
				instance.Spec.NetworkAttachments,
				serviceLabels,
				instance.Status.ReadyCount,
			)
			if err != nil {
				return ctrl.Result{}, err
			}
		} else {
			networkReady = true
		}

		instance.Status.NetworkAttachments = networkAttachmentStatus
		if networkReady {
			instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)
		} else {
			err := fmt.Errorf("%w: %s", designate.ErrNetworkAttachmentConfig, instance.Spec.NetworkAttachments)
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.NetworkAttachmentsReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.NetworkAttachmentsReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		// Mark the Deployment as Ready only if the number of Replicas is equals
		// to the Deployed instances (ReadyCount), and the the Status.Replicas
		// match Status.ReadyReplicas. If a deployment update is in progress,
		// Replicas > ReadyReplicas.
		// In addition, make sure the controller sees the last Generation
		// by comparing it with the ObservedGeneration.
		if statefulset.IsReady(deploy) {
			instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
		} else {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.DeploymentReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.DeploymentReadyRunningMessage))
		}
	}

	Log.Info("Reconciled single StatefulSet successfully")
	return ctrl.Result{}, nil
}

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

func (r *DesignateBackendbind9Reconciler) reconcileUpdate(ctx context.Context, instance *designatev1beta1.DesignateBackendbind9) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info(fmt.Sprintf("Reconciling Service '%s' update", instance.Name))

	// TODO: should have minor update tasks if required
	// - delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' update successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateBackendbind9Reconciler) reconcileUpgrade(ctx context.Context, instance *designatev1beta1.DesignateBackendbind9) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)
	Log.Info(fmt.Sprintf("Reconciling Service '%s' upgrade", instance.Name))

	// TODO: should have major version upgrade tasks
	// -delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' upgrade successfully", instance.Name))
	return ctrl.Result{}, nil
}

// generateServiceConfigMaps - create custom configmap to hold service-specific config
func (r *DesignateBackendbind9Reconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	envVars *map[string]env.Setter,
	serviceLabels map[string]string,
) error {
	Log := r.GetLogger(ctx)
	//
	// create custom Configmap for designate-backendbind9-specific config input
	// - %-config-data configmap holding custom config for the service's designate.conf
	//

	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.Name), serviceLabels)

	// customData hold any customization for the service.
	// custom.conf is going to be merged into /etc/designate/designate.conf.d/custom.conf
	// TODO: make sure custom.conf can not be overwritten
	customData := map[string]string{common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig}

	customData[common.CustomServiceConfigFileName] = instance.Spec.CustomServiceConfig

	var nadInfo *designate.NADConfig
	for _, netAtt := range instance.Spec.NetworkAttachments {
		nad, err := nad.GetNADWithName(ctx, h, netAtt, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				// Since the net-attach-def CR should have been manually created by the user and referenced in the spec,
				// we treat this as a warning because it means that the service will not be able to start.
				Log.Info(fmt.Sprintf("network-attachment-definition %s not found, cannot configure pod", netAtt))
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.ErrorReason,
					condition.SeverityWarning, // Severity is just warning because while we expect it, we will retry.
					condition.NetworkAttachmentsReadyErrorMessage,
					netAtt))
				return nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.NetworkAttachmentsReadyCondition,
				condition.ErrorReason,
				condition.SeverityError, // We cannot proceed with a broken network attachment.
				condition.NetworkAttachmentsReadyErrorMessage,
				err.Error()))
			return err
		}
		if nad.Name == instance.Spec.ControlNetworkName {
			nadInfo, err = designate.GetNADConfig(nad)
			if err != nil {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.ErrorReason,
					condition.SeverityError, // We cannot proceed with a broken network attachment.
					condition.NetworkAttachmentsReadyErrorMessage,
					err.Error()))
				return err
			}
			break
		}
	}
	if nadInfo == nil {
		return fmt.Errorf("%w: %s", designate.ErrNetworkAttachmentNotFound, instance.Spec.ControlNetworkName)
	}

	cidr := nadInfo.IPAM.CIDR.String()
	if cidr == "" {
		err := designate.ErrControlNetworkNotConfigured
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.NetworkAttachmentsReadyCondition,
			condition.ErrorReason,
			condition.SeverityError,
			condition.NetworkAttachmentsReadyErrorMessage,
			err))
		return err
	}
	templateParameters := make(map[string]any)
	if nadInfo.IPAM.CIDR.Addr().Is4() {
		templateParameters["IPVersion"] = "4"
	} else {
		templateParameters["IPVersion"] = "6"
	}
	templateParameters["AllowCIDR"] = cidr
	// This will need to be replaced by custom config for named.
	templateParameters["EnableQueryLogging"] = false
	templateParameters["CustomBindOptions"] = instance.Spec.CustomBindOptions

	// TODO: we need the rndc key value and pod addr but those are going to be supplied by the init container and
	// information mounted into the init container.

	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:         fmt.Sprintf("%s-scripts", instance.Name),
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeScripts,
			InstanceType: instance.Kind,
			AdditionalTemplate: map[string]string{
				"common.sh":     "/common/common.sh",
				"setipalias.py": "/common/setipalias.py",
			},
			Labels: cmLabels,
		},
		// Custom ConfigMap
		{
			Name:          fmt.Sprintf("%s-config-data", instance.Name),
			Namespace:     instance.Namespace,
			Type:          util.TemplateTypeConfig,
			InstanceType:  instance.Kind,
			CustomData:    customData,
			ConfigOptions: templateParameters,
			Labels:        cmLabels,
		},
		{
			Name:          fmt.Sprintf("%s-config-named", instance.Name),
			Namespace:     instance.Namespace,
			Type:          "config-named",
			InstanceType:  instance.Kind,
			ConfigOptions: templateParameters,
			Labels:        cmLabels,
		},
		{
			Name:          fmt.Sprintf("%s-default-overwrite", instance.Name),
			Namespace:     instance.Namespace,
			Type:          util.TemplateTypeNone,
			InstanceType:  instance.Kind,
			CustomData:    instance.Spec.DefaultConfigOverwrite,
			ConfigOptions: templateParameters,
			Labels:        cmLabels,
		},
	}

	return secret.EnsureSecrets(ctx, h, instance, cms, envVars)
}

// createHashOfInputHashes - creates a hash of hashes which gets added to the resources which requires a restart
// if any of the input resources change, like configs, passwords, ...
//
// returns the hash, whether the hash changed (as a bool) and any error
func (r *DesignateBackendbind9Reconciler) createHashOfInputHashes(
	ctx context.Context,
	instance *designatev1beta1.DesignateBackendbind9,
	envVars map[string]env.Setter,
) (string, bool, error) {
	Log := r.GetLogger(ctx)
	var hashMap map[string]string
	changed := false
	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	hash, err := util.ObjectHash(mergedMapVars)
	if err != nil {
		return hash, changed, err
	}
	if hashMap, changed = util.SetHash(instance.Status.Hash, common.InputHashName, hash); changed {
		instance.Status.Hash = hashMap
		Log.Info(fmt.Sprintf("Input maps hash %s - %s", common.InputHashName, hash))
	}
	return hash, changed, nil
}

func (r *DesignateBackendbind9Reconciler) hasMapChanged(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	mapName string,
	hashKey string,
) (bool, error) {
	Log := r.GetLogger(ctx)
	configMap := &corev1.ConfigMap{}
	err := h.GetClient().Get(ctx, types.NamespacedName{Name: mapName, Namespace: instance.GetNamespace()}, configMap)
	if err != nil {
		Log.Error(err, fmt.Sprintf("Unable to check config map %s for changes", mapName))
		return false, err
	}
	hashValue, err := configmap.Hash(configMap)
	if err != nil {
		return false, err
	}
	_, updated := util.SetHash(instance.Status.Hash, hashKey, hashValue)
	return updated, nil
}

func (r *DesignateBackendbind9Reconciler) hasSecretChanged(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.DesignateBackendbind9,
	secretName string,
	hashKey string,
) (bool, error) {
	Log := r.GetLogger(ctx)
	found := &corev1.Secret{}
	err := h.GetClient().Get(ctx, types.NamespacedName{Name: secretName, Namespace: instance.GetNamespace()}, found)
	if err != nil {
		Log.Error(err, fmt.Sprintf("Unable to check secret %s for changes", secretName))
		return false, err
	}
	hashValue, err := secret.Hash(found)
	if err != nil {
		return false, err
	}
	_, updated := util.SetHash(instance.Status.Hash, hashKey, hashValue)
	return updated, nil
}
