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

package controllers

import (
	"context"
	"fmt"
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
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	designatebackendbind9 "github.com/openstack-k8s-operators/designate-operator/pkg/designatebackendbind9"
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
		Owns(&corev1.Pod{}).
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

	// TODO(beagles): we really should create a single point of truth for what things are named or
	// what the names will be based on.
	serviceCount := min(int(*instance.Spec.Replicas), len(instance.Spec.Override.Services))
	for i := range serviceCount {
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

	// Define a new StatefulSet object
	deplDef, err := designatebackendbind9.StatefulSet(instance, inputHash, serviceLabels, serviceAnnotations, topology)
	if err != nil {
		return ctrl.Result{}, err
	}
	depl := statefulset.NewStatefulSet(
		deplDef,
		time.Duration(5)*time.Second,
	)

	ctrlResult, err = depl.CreateOrPatch(ctx, helper)
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
	// create StatefulSet - end

	// Handle pod labeling for predictable IPs
	config := designate.PodLabelingConfig{
		ConfigMapName: designate.BindPredIPConfigMap,
		IPKeyPrefix:   "bind_address_",
		ServiceName:   "designate-backendbind9",
	}
	err = designate.HandlePodLabeling(ctx, helper, instance.Name, instance.Namespace, config)
	if err != nil {
		Log.Error(err, "Failed to handle pod labeling")
		// Don't return error as this is not critical for the main reconcile loop
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
