/*
Copyright 2023.

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
	"time"

	"github.com/go-logr/logr"
	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designateunbound"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// UnboundReconciler -
type UnboundReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

func min(i int, j int) int {
	if i < j {
		return i
	}
	return j
}

//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch
//+kubebuilder:rbac:groups=topology.openstack.org,resources=topologies,verbs=get;list;watch;update

// Reconcile implementation for designate's Unbound resolver
func (r *UnboundReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	_ = r.Log.WithValues("unbound", req.NamespacedName)

	instance := &designatev1.DesignateUnbound{}
	err := r.Client.Get(ctx, req.NamespacedName, instance)
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
		r.Log,
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
			r.Log.Info(fmt.Sprintf("Panic during reconcile %v\n", rc))
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
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
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

	if instance.Status.NetworkAttachments == nil {
		instance.Status.NetworkAttachments = map[string][]string{}
	}

	// Init Topology condition if there's a reference
	if instance.Spec.TopologyRef != nil {
		c := condition.UnknownCondition(condition.TopologyReadyCondition, condition.InitReason, condition.TopologyReadyInitMessage)
		cl.Set(c)
	}

	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager for setting up the reconciler for the unbound resolver.
func (r *UnboundReconciler) SetupWithManager(mgr ctrl.Manager) error {

	// index topologyField
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &designatev1.DesignateUnbound{}, topologyField, func(rawObj client.Object) []string {
		// Extract the topology name from the spec, if one is provided
		cr := rawObj.(*designatev1.DesignateUnbound)
		if cr.Spec.TopologyRef == nil {
			return nil
		}
		return []string{cr.Spec.TopologyRef.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1.DesignateUnbound{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.StatefulSet{}).
		Watches(&topologyv1.Topology{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

func (r *UnboundReconciler) findObjectsForSrc(ctx context.Context, src client.Object) []reconcile.Request {
	requests := []reconcile.Request{}

	l := log.FromContext(ctx).WithName("Controllers").WithName("DesignateMdns")

	allWatchFields := []string{
		topologyField,
	}

	for _, field := range allWatchFields {
		crList := &designatev1.DesignateUnboundList{}
		listOps := &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(field, src.GetName()),
			Namespace:     src.GetNamespace(),
		}
		err := r.Client.List(context.TODO(), crList, listOps)
		if err != nil {
			l.Error(err, fmt.Sprintf("listing %s for field: %s - %s", crList.GroupVersionKind().Kind, field, src.GetNamespace()))
			return requests
		}

		for _, item := range crList.Items {
			l.Info(fmt.Sprintf("input source %s changed, reconcile: %s - %s", src.GetName(), item.GetName(), item.GetNamespace()))

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

func (r *UnboundReconciler) reconcileDelete(ctx context.Context, instance *designatev1.DesignateUnbound, helper *helper.Helper) (ctrl.Result, error) {
	util.LogForObject(helper, "Reconciling Service delete", instance)
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

	// Remove finalizer on the Topology CR
	if ctrlResult, err := topologyv1.EnsureDeletedTopologyRef(
		ctx,
		helper,
		instance.Status.LastAppliedTopology,
		instance.Name,
	); err != nil {
		return ctrlResult, err
	}
	util.LogForObject(helper, "Service deleted successfully", instance)
	return ctrl.Result{}, nil
}

func (r *UnboundReconciler) reconcileUpdate(instance *designatev1.DesignateUnbound, helper *helper.Helper) (ctrl.Result, error) {
	util.LogForObject(helper, "Reconciling service update", instance)
	util.LogForObject(helper, "Service updated successfully", instance)
	return ctrl.Result{}, nil
}

func (r *UnboundReconciler) reconcileUpgrade(instance *designatev1.DesignateUnbound, helper *helper.Helper) (ctrl.Result, error) {
	util.LogForObject(helper, "Reconciling service update", instance)
	util.LogForObject(helper, "Service updated successfully", instance)
	return ctrl.Result{}, nil
}

func (r *UnboundReconciler) reconcileNormal(ctx context.Context, instance *designatev1.DesignateUnbound, helper *helper.Helper) (ctrl.Result, error) {
	util.LogForObject(helper, "Reconciling Service", instance)

	if controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		// Return to persist the finalizer immediately
		return ctrl.Result{}, nil
	}

	serviceLabels := map[string]string{
		common.AppSelector:       instance.ObjectMeta.Name,
		common.ComponentSelector: designateunbound.Component,
	}

	serviceCount := min(int(*instance.Spec.Replicas), len(instance.Spec.Override.Services))
	for i := 0; i < serviceCount; i++ {
		svc, err := designate.CreateDNSService(
			fmt.Sprintf("designate-unbound-%d", i),
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

	configMapVars := make(map[string]env.Setter)
	err := r.generateServiceConfigMaps(ctx, instance, helper, &configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	//
	// create hash over all the different input resources to identify if any those changed
	// and a restart/recreate is required.
	//
	inputHash, hashChanged, err := r.createHashOfInputHashes(instance, configMapVars)
	if err != nil {
		return ctrl.Result{}, err
	} else if hashChanged {
		// Hash changed and instance status should be updated (which will be done by main defer func),
		// so we need to return and reconcile again
		return ctrl.Result{}, nil
	}

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	nadList := []networkv1.NetworkAttachmentDefinition{}
	for _, networkAttachment := range instance.Spec.NetworkAttachments {
		nad, err := nad.GetNADWithName(ctx, helper, networkAttachment, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				r.Log.Info(fmt.Sprintf("network-attachment-definition %s not found", networkAttachment))
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					condition.NetworkAttachmentsReadyWaitingMessage,
					networkAttachment))
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

	// Handle service update
	ctrlResult, err := r.reconcileUpdate(instance, helper)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Handle service upgrade
	ctrlResult, err = r.reconcileUpgrade(instance, helper)
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

	// Define a new Unbound StatefulSet object
	statefulSetDef := designateunbound.StatefulSet(instance, inputHash, serviceLabels, serviceAnnotations, topology)
	statefulSet := statefulset.NewStatefulSet(
		statefulSetDef,
		time.Duration(5)*time.Second,
	)

	r.Log.Info("deploying the unbound pod")

	ctrlResult, err = statefulSet.CreateOrPatch(ctx, helper)
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

	deploy := statefulSet.GetStatefulSet()
	if deploy.Generation == deploy.Status.ObservedGeneration {
		instance.Status.ReadyCount = deploy.Status.ReadyReplicas

		r.Log.Info("verifying network attachments")
		// verify if network attachment matches expectations
		networkReady := false
		networkAttachmentStatus := map[string][]string{}
		if *instance.Spec.Replicas > 0 {
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
			err := fmt.Errorf("not all pods have interfaces with ips as configured in NetworkAttachments: %s", instance.Spec.NetworkAttachments)
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

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
	}
	r.Log.Info("Reconciled Service successfully")
	return r.onIPChange()
}

func (r *UnboundReconciler) onIPChange() (ctrl.Result, error) {
	//
	// TODO(beagles): neutron should be configured with the unbound POD
	// endpoints. If these change we'll need to update neutron configuration.
	// Ideally we'd cache neutron's original configuration in a secret so we
	// can return it to it's original state if we are deleteing the unbound
	// pods.
	//
	return ctrl.Result{}, nil
}

func (r *UnboundReconciler) generateServiceConfigMaps(
	ctx context.Context,
	instance *designatev1.DesignateUnbound,
	h *helper.Helper,
	envVars *map[string]env.Setter,
) error {
	r.Log.Info("Generating service config map")
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(designateunbound.Component), map[string]string{})
	customData := map[string]string{common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig}
	for key, data := range instance.Spec.DefaultConfigOverwrite {
		customData[key] = data
	}

	templateParameters := make(map[string]interface{})

	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:               fmt.Sprintf("%s-scripts", instance.Name),
			Namespace:          instance.Namespace,
			Type:               util.TemplateTypeScripts,
			InstanceType:       instance.Kind,
			AdditionalTemplate: map[string]string{"common.sh": "/common/common.sh"},
			Labels:             cmLabels,
		},
		// ConfigMap
		{
			Name:          fmt.Sprintf("%s-config-data", instance.Name),
			Namespace:     instance.Namespace,
			Type:          util.TemplateTypeConfig,
			InstanceType:  instance.Kind,
			CustomData:    customData,
			ConfigOptions: templateParameters,
			Labels:        cmLabels,
		},
	}
	err := secret.EnsureSecrets(ctx, h, instance, cms, envVars)

	if err != nil {
		r.Log.Error(err, "uanble to process config map")
		return err
	}
	r.Log.Info("Service config map generated")
	return nil
}

func (r *UnboundReconciler) createHashOfInputHashes(
	instance *designatev1.DesignateUnbound,
	envVars map[string]env.Setter,
) (string, bool, error) {
	r.Log.Info("Creating hash of inputs")
	var hashMap map[string]string
	changed := false
	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	hash, err := util.ObjectHash(mergedMapVars)
	if err != nil {
		return hash, changed, err
	}
	if hashMap, changed = util.SetHash(instance.Status.Hash, common.InputHashName, hash); changed {
		instance.Status.Hash = hashMap
		r.Log.Info(fmt.Sprintf("Input maps hash %s - %s", common.InputHashName, hash))
	}
	r.Log.Info("Input hash created/updated")
	return hash, changed, nil
}
