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
// FIXME(dkehn): Adjust this file.

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
	"k8s.io/utils/ptr"
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
	designateapi "github.com/openstack-k8s-operators/designate-operator/pkg/designateapi"
	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/deployment"
	"github.com/openstack-k8s-operators/lib-common/modules/common/endpoint"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	oko_secret "github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

// GetClient -
func (r *DesignateAPIReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *DesignateAPIReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetScheme -
func (r *DesignateAPIReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// DesignateAPIReconciler reconciles a DesignateAPI object
type DesignateAPIReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Scheme  *runtime.Scheme
}

// GetLogger returns a logger object with a prefix of "controller.name" and additional controller context fields
func (r *DesignateAPIReconciler) GetLogger(ctx context.Context) logr.Logger {
	return log.FromContext(ctx).WithName("Controllers").WithName("DesignateAPI")
}

var keystoneServices = []map[string]string{
	{
		"type": designate.ServiceType,
		"name": designate.ServiceName,
		"desc": "Designate Service",
	},
}

//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/finalizers,verbs=update;patch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;
// +kubebuilder:rbac:groups=route.openshift.io,resources=routes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=keystone.openstack.org,resources=keystoneservices,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keystone.openstack.org,resources=keystoneendpoints,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DesignateAPI object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *DesignateAPIReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	Log := r.GetLogger(ctx)

	// Fetch the DesignateAPI instance
	instance := &designatev1beta1.DesignateAPI{}
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
		condition.UnknownCondition(condition.CreateServiceReadyCondition, condition.InitReason, condition.CreateServiceReadyInitMessage),
		condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
		condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		// right now we have no dedicated KeystoneServiceReadyInitMessage
		condition.UnknownCondition(condition.KeystoneServiceReadyCondition, condition.InitReason, ""),
		condition.UnknownCondition(condition.KeystoneEndpointReadyCondition, condition.InitReason, ""),
		condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
		condition.UnknownCondition(condition.TLSInputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
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
	if instance.Status.APIEndpoints == nil {
		instance.Status.APIEndpoints = map[string]map[string]string{}
	}
	if instance.Status.NetworkAttachments == nil {
		instance.Status.NetworkAttachments = map[string][]string{}
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DesignateAPIReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	// Watch for changes to any CustomServiceConfigSecrets. Global secrets
	// (e.g. TransportURLSecret) are handled by the top designate controller.
	Log := r.GetLogger(ctx)
	if err := mgr.GetFieldIndexer().IndexField(ctx, &designatev1beta1.DesignateAPI{}, passwordSecretField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*designatev1beta1.DesignateAPI)
		if cr.Spec.Secret == "" {
			return nil
		}
		return []string{cr.Spec.Secret}
	}); err != nil {
		return err
	}

	// index caBundleSecretNameField
	if err := mgr.GetFieldIndexer().IndexField(ctx, &designatev1beta1.DesignateAPI{}, caBundleSecretNameField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*designatev1beta1.DesignateAPI)
		if cr.Spec.TLS.CaBundleSecretName == "" {
			return nil
		}
		return []string{cr.Spec.TLS.CaBundleSecretName}
	}); err != nil {
		return err
	}

	// index tlsAPIInternalField
	if err := mgr.GetFieldIndexer().IndexField(ctx, &designatev1beta1.DesignateAPI{}, tlsAPIInternalField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*designatev1beta1.DesignateAPI)
		if cr.Spec.TLS.API.Internal.SecretName == nil {
			return nil
		}
		return []string{*cr.Spec.TLS.API.Internal.SecretName}
	}); err != nil {
		return err
	}

	// index tlsAPIPublicField
	if err := mgr.GetFieldIndexer().IndexField(ctx, &designatev1beta1.DesignateAPI{}, tlsAPIPublicField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*designatev1beta1.DesignateAPI)
		if cr.Spec.TLS.API.Public.SecretName == nil {
			return nil
		}
		return []string{*cr.Spec.TLS.API.Public.SecretName}
	}); err != nil {
		return err
	}

	svcSecretFn := func(ctx context.Context, o client.Object) []reconcile.Request {
		var namespace string = o.GetNamespace()
		var secretName string = o.GetName()
		result := []reconcile.Request{}

		// get all API CRs
		apis := &designatev1beta1.DesignateAPIList{}
		listOpts := []client.ListOption{
			client.InNamespace(namespace),
		}
		if err := r.Client.List(ctx, apis, listOpts...); err != nil {
			Log.Error(err, "Unable to retrieve API CRs")
			return nil
		}
		for _, cr := range apis.Items {
			for _, v := range cr.Spec.CustomServiceConfigSecrets {
				if v == secretName {
					name := client.ObjectKey{
						Namespace: namespace,
						Name:      cr.Name,
					}
					Log.Info(fmt.Sprintf("Secret %s is used by Designate CR %s", secretName, cr.Name))
					result = append(result, reconcile.Request{NamespacedName: name})
				}
			}
		}
		if len(result) > 0 {
			return result
		}
		return nil
	}

	// watch for configmap where the CM owner label AND the CR.Spec.ManagingCrName label matches
	// Watch for changes to NADs
	// NOTE: Dkehn/DPrince are configMap and NAD one in the same??
	nadFn := func(ctx context.Context, o client.Object) []reconcile.Request {
		result := []reconcile.Request{}
		// configMapFn := func(o client.Object) []reconcile.Request {
		// 	result := []reconcile.Request{}

		// get all API CRs
		designateAPIs := &designatev1beta1.DesignateAPIList{}
		listOpts := []client.ListOption{
			client.InNamespace(o.GetNamespace()),
		}
		if err := r.Client.List(ctx, designateAPIs, listOpts...); err != nil {
			Log.Error(err, "Unable to retrieve DesignateAPI CRs %v")
			return nil
		}

		for _, cr := range designateAPIs.Items {
			if util.StringInSlice(o.GetName(), cr.Spec.NetworkAttachments) {
				name := client.ObjectKey{
					Namespace: cr.GetNamespace(),
					Name:      cr.GetName(),
				}
				Log.Info(fmt.Sprintf("NAD %s is used by DesignateAPI CR %s", o.GetName(), cr.GetName()))
				result = append(result, reconcile.Request{NamespacedName: name})
			}
		}

		if len(result) > 0 {
			return result
		}
		return nil
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1beta1.DesignateAPI{}).
		Owns(&keystonev1.KeystoneService{}).
		Owns(&keystonev1.KeystoneEndpoint{}).
		Owns(&corev1.Service{}).
		Owns(&appsv1.Deployment{}).
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(svcSecretFn)).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Watches(&networkv1.NetworkAttachmentDefinition{},
			handler.EnqueueRequestsFromMapFunc(nadFn)).
		Complete(r)
}

func (r *DesignateAPIReconciler) findObjectsForSrc(ctx context.Context, src client.Object) []reconcile.Request {
	requests := []reconcile.Request{}

	l := log.FromContext(ctx).WithName("Controllers").WithName("DesignateAPI")

	allWatchFields := []string{
		passwordSecretField,
		caBundleSecretNameField,
		tlsAPIInternalField,
		tlsAPIPublicField,
	}

	for _, field := range allWatchFields {
		crList := &designatev1beta1.DesignateAPIList{}
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

func (r *DesignateAPIReconciler) reconcileDelete(ctx context.Context, instance *designatev1beta1.DesignateAPI, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' delete", instance.Name))

	for _, ksSvc := range keystoneServices {

		// Remove the finalizer from our KeystoneEndpoint CR
		keystoneEndpoint, err := keystonev1.GetKeystoneEndpointWithName(ctx, helper, ksSvc["name"], instance.Namespace)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if err == nil {
			controllerutil.RemoveFinalizer(keystoneEndpoint, helper.GetFinalizer())
			if err = helper.GetClient().Update(ctx, keystoneEndpoint); err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			util.LogForObject(helper, "Removed finalizer from our KeystoneEndpoint", instance)
		}

		// Remove the finalizer from our KeystoneService CR
		keystoneService, err := keystonev1.GetKeystoneServiceWithName(ctx, helper, ksSvc["name"], instance.Namespace)
		if err != nil && !k8s_errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}

		if err == nil {
			controllerutil.RemoveFinalizer(keystoneService, helper.GetFinalizer())
			if err = helper.GetClient().Update(ctx, keystoneService); err != nil && !k8s_errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			util.LogForObject(helper, "Removed finalizer from our KeystoneService", instance)
		}
	}

	// We did all the cleanup on the objects we created so we can remove the
	// finalizer from ourselves to allow the deletion
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	Log.Info(fmt.Sprintf("Reconciled Service '%s' delete successfully", instance.Name))

	return ctrl.Result{}, nil
}

func (r *DesignateAPIReconciler) reconcileInit(
	ctx context.Context,
	instance *designatev1beta1.DesignateAPI,
	helper *helper.Helper,
	serviceLabels map[string]string,
) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' init", instance.Name))

	//
	// expose the service (create service, route and return the created endpoint URLs)
	//

	// Endpoint
	publicEndpointData := endpoint.Data{
		Port: designate.DesignatePublicPort,
		Path: "/",
	}
	internalEndpointData := endpoint.Data{
		Port: designate.DesignateInternalPort,
		Path: "/",
	}
	designateEndpoints := map[service.Endpoint]endpoint.Data{
		service.EndpointPublic:   publicEndpointData,
		service.EndpointInternal: internalEndpointData,
	}

	apiEndpoints := make(map[string]string)

	for endpointType, data := range designateEndpoints {
		endpointTypeStr := string(endpointType)
		endpointName := designate.ServiceName + "-" + endpointTypeStr
		svcOverride := instance.Spec.Override.Service[endpointType]
		if svcOverride.EmbeddedLabelsAnnotations == nil {
			svcOverride.EmbeddedLabelsAnnotations = &service.EmbeddedLabelsAnnotations{}
		}

		exportLabels := util.MergeStringMaps(
			serviceLabels,
			map[string]string{
				service.AnnotationEndpointKey: endpointTypeStr,
			},
		)

		// Create the service
		svc, err := service.NewService(
			service.GenericService(&service.GenericServiceDetails{
				Name:      endpointName,
				Namespace: instance.Namespace,
				Labels:    exportLabels,
				Selector:  serviceLabels,
				Port: service.GenericServicePort{
					Name:     endpointName,
					Port:     data.Port,
					Protocol: corev1.ProtocolTCP,
				},
			}),
			5,
			&svcOverride.OverrideSpec,
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

		svc.AddAnnotation(map[string]string{
			service.AnnotationEndpointKey: endpointTypeStr,
		})

		// add Annotation to whether creating an ingress is required or not
		if endpointType == service.EndpointPublic && svc.GetServiceType() == corev1.ServiceTypeClusterIP {
			svc.AddAnnotation(map[string]string{
				service.AnnotationIngressCreateKey: "true",
			})
		} else {
			svc.AddAnnotation(map[string]string{
				service.AnnotationIngressCreateKey: "false",
			})
			if svc.GetServiceType() == corev1.ServiceTypeLoadBalancer {
				svc.AddAnnotation(map[string]string{
					service.AnnotationHostnameKey: svc.GetServiceHostname(), // add annotation to register service name in dnsmasq
				})
			}
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
		} else if (ctrlResult != ctrl.Result{}) {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.CreateServiceReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.CreateServiceReadyRunningMessage))
			return ctrlResult, nil
		}
		// create service - end

		// if TLS is enabled
		if instance.Spec.TLS.API.Enabled(endpointType) {
			// set endpoint protocol to https
			data.Protocol = ptr.To(service.ProtocolHTTPS)
		}
		apiEndpoints[string(endpointType)], err = svc.GetAPIEndpoint(
			svcOverride.EndpointURL, data.Protocol, data.Path)
		if err != nil {
			return ctrl.Result{}, err
		}
	}

	// Endpoint - end

	instance.Status.Conditions.MarkTrue(condition.CreateServiceReadyCondition, condition.CreateServiceReadyMessage)

	// expose service - end

	//
	// Update instance status with service endpoint url from route host information
	//
	if instance.Status.APIEndpoints == nil {
		instance.Status.APIEndpoints = map[string]map[string]string{}
	}
	// instance.Status.APIEndpoints = apiEndpoints
	instance.Status.APIEndpoints[designate.ServiceName] = apiEndpoints

	//
	// create service and user in keystone - - https://docs.openstack.org/Designate/latest/install/install-rdo.html#configure-user-and-endpoints
	// TODO: rework this
	for _, ksSvc := range keystoneServices {
		ksSvcSpec := keystonev1.KeystoneServiceSpec{
			ServiceType:        ksSvc["type"],
			ServiceName:        ksSvc["name"],
			ServiceDescription: ksSvc["desc"],
			Enabled:            true,
			ServiceUser:        instance.Spec.ServiceUser,
			Secret:             instance.Spec.Secret,
			PasswordSelector:   instance.Spec.PasswordSelectors.Service,
		}

		ksSvcObj := keystonev1.NewKeystoneService(ksSvcSpec, instance.Namespace, serviceLabels, time.Duration(10)*time.Second)
		ctrlResult, err := ksSvcObj.CreateOrPatch(ctx, helper)
		if err != nil {
			return ctrlResult, err
		}

		// mirror the Status, Reason, Severity and Message of the latest keystoneservice condition
		// into a local condition with the type condition.KeystoneServiceReadyCondition
		c := ksSvcObj.GetConditions().Mirror(condition.KeystoneServiceReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}

		if (ctrlResult != ctrl.Result{}) {
			return ctrlResult, nil
		}

		ksEndptSpec := keystonev1.KeystoneEndpointSpec{
			ServiceName: ksSvc["name"],
			Endpoints:   apiEndpoints,
		}

		ksEndptObj := keystonev1.NewKeystoneEndpoint(
			ksSvc["name"],
			instance.Namespace,
			ksEndptSpec,
			serviceLabels,
			time.Duration(10)*time.Second)
		ctrlResult, err = ksEndptObj.CreateOrPatch(ctx, helper)
		if err != nil {
			return ctrlResult, err
		}

		// mirror the Status, Reason, Severity and Message of the latest keystoneendpoint condition
		// into a local condition with the type condition.KeystoneEndpointReadyCondition
		c = ksEndptObj.GetConditions().Mirror(condition.KeystoneEndpointReadyCondition)
		if c != nil {
			instance.Status.Conditions.Set(c)
		}

		if (ctrlResult != ctrl.Result{}) {
			return ctrl.Result{}, nil
		}
	}

	Log.Info(fmt.Sprintf("Reconciled Service '%s' init successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateAPIReconciler) reconcileNormal(ctx context.Context, instance *designatev1beta1.DesignateAPI, helper *helper.Helper) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info("Reconciling Service")

	// ConfigMap
	configMapVars := make(map[string]env.Setter)

	//
	// check for required OpenStack secret holding passwords for service/admin user and add hash to the vars map
	//
	ctrlResult, err := r.getSecret(ctx, helper, instance, instance.Spec.Secret, &configMapVars, "secret-")
	if err != nil {
		return ctrlResult, err
	}

	// run check OpenStack secret - end

	//
	// TLS input validation
	//
	// Validate the CA cert secret if provided
	if instance.Spec.TLS.CaBundleSecretName != "" {
		hash, err := tls.ValidateCACertSecret(
			ctx,
			helper.GetClient(),
			types.NamespacedName{
				Name:      instance.Spec.TLS.CaBundleSecretName,
				Namespace: instance.Namespace,
			},
		)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					fmt.Sprintf(condition.TLSInputReadyWaitingMessage, instance.Spec.TLS.CaBundleSecretName)))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		if hash != "" {
			configMapVars[tls.CABundleKey] = env.SetValue(hash)
		}

		// Validate API service certs secrets
		certsHash, err := instance.Spec.TLS.API.ValidateCertSecrets(ctx, helper, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					fmt.Sprintf(condition.TLSInputReadyWaitingMessage, err.Error())))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		configMapVars[tls.TLSHashName] = env.SetValue(certsHash)
	}

	// all cert input checks out so report InputReady
	instance.Status.Conditions.MarkTrue(condition.TLSInputReadyCondition, condition.InputReadyMessage)

	//
	// check for required TransportURL secret holding transport URL string
	//
	// ctrlResult, err = r.getSecret(ctx, helper, instance, instance.Spec.TransportURLSecret, &configMapVars, "secret-")
	// if err != nil {
	// 	return ctrlResult, err
	// }
	// run check TransportURL secret - end

	//
	// check for required service secrets
	//
	for _, secretName := range instance.Spec.CustomServiceConfigSecrets {
		ctrlResult, err = r.getSecret(ctx, helper, instance, secretName, &configMapVars, "secret-")
		if err != nil {
			return ctrlResult, err
		}
	}
	// run check service secrets - end

	//
	// check for required Designate config maps that should have been created by parent Designate CR
	//

	parentDesignateName := designate.GetOwningDesignateName(instance)

	ctrlResult, err = r.getSecret(ctx, helper, instance, fmt.Sprintf("%s-scripts", parentDesignateName), &configMapVars, "")
	if err != nil {
		return ctrlResult, err
	}
	ctrlResult, err = r.getSecret(ctx, helper, instance, fmt.Sprintf("%s-config-data", parentDesignateName), &configMapVars, "")
	// note r.getSecret adds Conditions with condition.InputReadyWaitingMessage
	// when secret is not found
	if err != nil {
		return ctrlResult, err
	}

	// run check parent Designate CR config maps - end

	//
	// Create ConfigMaps required as input for the Service and calculate an overall hash of hashes
	//

	serviceLabels := map[string]string{
		common.AppSelector:       designate.ServiceName,
		common.ComponentSelector: designateapi.Component,
	}

	//
	// create custom Configmap for this designate volume service
	//
	err = r.generateServiceConfigMaps(ctx, helper, instance, &configMapVars)
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

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	// Create ConfigMaps and Secrets - end

	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	//
	// TODO check when/if Init, Update, or Upgrade should/could be skipped
	//
	// networks to attach to
	nadList := []networkv1.NetworkAttachmentDefinition{}
	for _, netAtt := range instance.Spec.NetworkAttachments {
		nad, err := nad.GetNADWithName(ctx, helper, netAtt, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				Log.Info(fmt.Sprintf("network-attachment-definition %s not found", netAtt))
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
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
	ctrlResult, err = r.reconcileInit(ctx, instance, helper, serviceLabels)
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

	// Define a new Deployment object
	deplDef, err := designateapi.Deployment(instance, inputHash, serviceLabels, serviceAnnotations)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DeploymentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DeploymentReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	depl := deployment.NewDeployment(
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
	if depl.GetDeployment().Generation == depl.GetDeployment().Status.ObservedGeneration {
		instance.Status.ReadyCount = depl.GetDeployment().Status.ReadyReplicas

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
	Log.Info("Reconciled Service successfully")
	return ctrl.Result{}, nil
}

func (r *DesignateAPIReconciler) reconcileUpdate(ctx context.Context, instance *designatev1beta1.DesignateAPI) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' update", instance.Name))

	// TODO: should have minor update tasks if required
	// - delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' update successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateAPIReconciler) reconcileUpgrade(ctx context.Context, instance *designatev1beta1.DesignateAPI) (ctrl.Result, error) {
	Log := r.GetLogger(ctx)

	Log.Info(fmt.Sprintf("Reconciling Service '%s' upgrade", instance.Name))

	// TODO: should have major version upgrade tasks
	// -delete dbsync hash from status to rerun it?

	Log.Info(fmt.Sprintf("Reconciled Service '%s' upgrade successfully", instance.Name))
	return ctrl.Result{}, nil
}

// getSecret - get the specified secret, and add its hash to envVars
func (r *DesignateAPIReconciler) getSecret(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.DesignateAPI,
	secretName string,
	envVars *map[string]env.Setter,
	prefix string,
) (ctrl.Result, error) {
	secret, hash, err := oko_secret.GetSecret(ctx, h, secretName, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			h.GetLogger().Info(fmt.Sprintf("Secret %s not found", secretName))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.InputReadyWaitingMessage))
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
		}
		instance.Status.Conditions.Set(condition.FalseCondition(
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

// generateServiceConfigMaps - create custom configmap to hold service-specific config
// TODO add DefaultConfigOverwrite
func (r *DesignateAPIReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.DesignateAPI,
	envVars *map[string]env.Setter,
) error {
	Log := r.GetLogger(ctx)
	Log.Info("Generating service config map")

	//
	// create custom Configmap for designate-api-specific config input
	// - %-config-data configmap holding custom config for the service's designate.conf
	//

	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(designate.ServiceName), map[string]string{})

	db, err := mariadbv1.GetDatabaseByNameAndAccount(ctx, h, designate.DatabaseName, instance.Spec.DatabaseAccount, instance.Namespace)
	if err != nil {
		return err
	}
	var tlsCfg *tls.Service
	if instance.Spec.TLS.CaBundleSecretName != "" {
		tlsCfg = &tls.Service{}
	}

	// customData hold any customization for the service.
	// custom.conf is going to be merged into /etc/designate/conder.conf
	// TODO: make sure custom.conf can not be overwritten
	customData := map[string]string{
		common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig,
		"my.cnf":                           db.GetDatabaseClientConfig(tlsCfg), //(oschwart) for now just get the default my.cnf
	}

	for key, data := range instance.Spec.DefaultConfigOverwrite {
		customData[key] = data
	}

	keystoneAPI, err := keystonev1.GetKeystoneAPI(ctx, h, instance.Namespace, map[string]string{})
	if err != nil {
		return err
	}
	keystoneInternalURL, err := keystoneAPI.GetEndpoint(endpoint.EndpointInternal)
	if err != nil {
		return err
	}
	keystonePublicURL, err := keystoneAPI.GetEndpoint(endpoint.EndpointPublic)
	if err != nil {
		return err
	}

	customData[common.CustomServiceConfigFileName] = instance.Spec.CustomServiceConfig

	databaseAccount, dbSecret, err := mariadbv1.GetAccountAndSecret(
		ctx, h, instance.Spec.DatabaseAccount, instance.Namespace)

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			mariadbv1.MariaDBAccountReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			mariadbv1.MariaDBAccountNotReadyMessage,
			err.Error()))

		return err
	}

	instance.Status.Conditions.MarkTrue(
		mariadbv1.MariaDBAccountReadyCondition,
		mariadbv1.MariaDBAccountReadyMessage)

	templateParameters := map[string]interface{}{
		"DatabaseConnection": fmt.Sprintf("mysql+pymysql://%s:%s@%s/%s?read_default_file=/etc/my.cnf",
			databaseAccount.Spec.UserName,
			string(dbSecret.Data[mariadbv1.DatabasePasswordSelector]),
			instance.Spec.DatabaseHostname,
			designate.DatabaseName,
		),
	}

	templateParameters["ServiceUser"] = instance.Spec.ServiceUser
	templateParameters["KeystoneInternalURL"] = keystoneInternalURL
	templateParameters["KeystonePublicURL"] = keystonePublicURL
	templateParameters["TimeOut"] = instance.Spec.APITimeout

	// create httpd  vhost template parameters
	httpdVhostConfig := map[string]interface{}{}
	for _, endpt := range []service.Endpoint{service.EndpointInternal, service.EndpointPublic} {
		endptConfig := map[string]interface{}{}
		endptConfig["ServerName"] = fmt.Sprintf("%s-%s.%s.svc", designate.ServiceName, endpt.String(), instance.Namespace)
		endptConfig["TLS"] = false // default TLS to false, and set it bellow to true if enabled
		if instance.Spec.TLS.API.Enabled(endpt) {
			endptConfig["TLS"] = true
			endptConfig["SSLCertificateFile"] = fmt.Sprintf("/etc/pki/tls/certs/%s.crt", endpt.String())
			endptConfig["SSLCertificateKeyFile"] = fmt.Sprintf("/etc/pki/tls/private/%s.key", endpt.String())
		}
		httpdVhostConfig[endpt.String()] = endptConfig
	}
	templateParameters["VHosts"] = httpdVhostConfig

	transportURLSecret, _, err := oko_secret.GetSecret(ctx, h, instance.Spec.TransportURLSecret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			Log.Info(fmt.Sprintf("TransportURL secret %s not found", instance.Spec.TransportURLSecret))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
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

	adminPasswordSecret, _, err := oko_secret.GetSecret(ctx, h, instance.Spec.Secret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			Log.Info(fmt.Sprintf("AdminPassword secret %s not found", instance.Spec.Secret))
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
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
	}

	err = oko_secret.EnsureSecrets(ctx, h, instance, cms, envVars)

	if err != nil {
		Log.Error(err, "unable to process config map")
		return err
	}
	Log.Info("Service config map generated")

	return nil
}

// createHashOfInputHashes - creates a hash of hashes which gets added to the resources which requires a restart
// if any of the input resources change, like configs, passwords, ...
//
// returns the hash, whether the hash changed (as a bool) and any error
func (r *DesignateAPIReconciler) createHashOfInputHashes(
	ctx context.Context,
	instance *designatev1beta1.DesignateAPI,
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
