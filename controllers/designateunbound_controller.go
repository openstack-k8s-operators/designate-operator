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
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designateunbound"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/deployment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
)

// UnboundReconciler -
type UnboundReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateunbounds/finalizers,verbs=update;patch
//+kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch

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

	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(instance, helper)
	}

	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager for setting up the reconciler for the unbound resolver.
func (r *UnboundReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1.DesignateUnbound{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func (r *UnboundReconciler) reconcileDelete(instance *designatev1.DesignateUnbound, helper *helper.Helper) (ctrl.Result, error) {
	util.LogForObject(helper, "Reconciling Service delete", instance)
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())

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

	exportLabels := labels.GetLabels(instance, designateunbound.ServiceName, map[string]string{
		"owner": "designate",
		"crc":   instance.GetName(),
		"app":   designateunbound.ServiceName,
	})

	serviceLabels := map[string]string{
		common.AppSelector: designateunbound.ServiceName,
	}

	// XXX(beagles) I think this is wrong - in as sense. It's not a traditional
	// service endpoint in that we might want to have an externally addressable
	// IP for each pod. I don't know what the pattern is for that.

	svc, err := service.NewService(
		service.GenericService(&service.GenericServiceDetails{
			Name:      instance.GetName(),
			Namespace: instance.Namespace,
			Labels:    exportLabels,
			Selector:  serviceLabels,
			Port: service.GenericServicePort{
				Name:     "designate-unbound",
				Port:     53,
				Protocol: corev1.ProtocolTCP,
			},
		}),
		time.Duration(5)*time.Second,
		&service.OverrideSpec{}, // XXX(beagles): this should be something real. Check other service definitions.
	)

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	// XXX annotations?

	ctrlResult, err := svc.CreateOrPatch(ctx, helper)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ExposeServiceReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ExposeServiceReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	}
	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	configMapVars := make(map[string]env.Setter)
	err = r.generateServiceConfigMaps(ctx, instance, helper, &configMapVars)
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

	for _, networkAttachment := range instance.Spec.NetworkAttachments {
		_, err := nad.GetNADWithName(ctx, helper, networkAttachment, instance.Namespace)
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
	}

	serviceAnnotations, err := nad.CreateNetworksAnnotation(instance.Namespace, instance.Spec.NetworkAttachments)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed create network annotation from %s: %w",
			instance.Spec.NetworkAttachments, err)
	}

	// Handle service update
	ctrlResult, err = r.reconcileUpdate(instance, helper)
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

	depl := deployment.NewDeployment(
		designateunbound.Deployment(instance, inputHash, serviceLabels, serviceAnnotations),
		time.Duration(5)*time.Second,
	)

	r.Log.Info("deploying the unbound pod")

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

	if depl.GetDeployment().Generation == depl.GetDeployment().Status.ObservedGeneration {
		instance.Status.ReadyCount = depl.GetDeployment().Status.ReadyReplicas

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
		if instance.Status.ReadyCount == *instance.Spec.Replicas {
			instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)
		}
	}
	// create Deployment - end

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
	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(designateunbound.ServiceName), map[string]string{})
	customData := map[string]string{common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig}
	for key, data := range instance.Spec.DefaultConfigOverwrite {
		customData[key] = data
	}

	templateParameters := make(map[string]interface{})
	// TODO(beagles): these are defaulting to everything and might actually be fine because of how this
	// is addressed but the network cidr should be derivable if there are network attachments ... I think.
	templateParameters["ListenIP"] = "0.0.0.0"
	templateParameters["ExternalNetCidr"] = "0.0.0.0/0"

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
