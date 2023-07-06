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

package controllers

import (
	"context"
	"fmt"
	"time"

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/go-logr/logr"
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	rabbitmqv1 "github.com/openstack-k8s-operators/infra-operator/apis/rabbitmq/v1beta1"
	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	"github.com/openstack-k8s-operators/lib-common/modules/common/endpoint"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/job"
	"github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	nad "github.com/openstack-k8s-operators/lib-common/modules/common/networkattachment"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	"github.com/openstack-k8s-operators/lib-common/modules/database"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetClient -
func (r *DesignateReconciler) GetClient() client.Client {
	return r.Client
}

// GetKClient -
func (r *DesignateReconciler) GetKClient() kubernetes.Interface {
	return r.Kclient
}

// GetLogger -
func (r *DesignateReconciler) GetLogger() logr.Logger {
	return r.Log
}

// GetScheme -
func (r *DesignateReconciler) GetScheme() *runtime.Scheme {
	return r.Scheme
}

// DesignateReconciler reconciles a Designate object
type DesignateReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designates/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateapis/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatecentrals/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateproducers/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateagents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateagents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateagents/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designateworkers/finalizers,verbs=update
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdns,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdns/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=designate.openstack.org,resources=designatemdns/finalizers,verbs=update

// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;create;update;patch;delete;watch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=mariadbdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=keystone.openstack.org,resources=keystoneapis,verbs=get;list;watch
// +kubebuilder:rbac:groups=rabbitmq.openstack.org,resources=transporturls,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=k8s.cni.cncf.io,resources=network-attachment-definitions,verbs=get;list;watch

// service account, role, rolebinding
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update
// service account permissions that are needed to grant permission to the above
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid;privileged,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

// Reconcile -
func (r *DesignateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	_ = log.FromContext(ctx)

	// Fetch the Designate instance
	instance := &designatev1beta1.Designate{}
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

	// Always patch the instance status when exiting this function so we can persist any changes.
	defer func() {
		// update the overall status condition if service is ready
		if instance.IsReady() {
			instance.Status.Conditions.MarkTrue(condition.ReadyCondition, condition.ReadyMessage)
		}

		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) {
		return ctrl.Result{}, nil
	}

	//
	// initialize status
	//
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
		// initialize conditions used later as Status=Unknown
		cl := condition.CreateList(
			condition.UnknownCondition(condition.DBReadyCondition, condition.InitReason, condition.DBReadyInitMessage),
			condition.UnknownCondition(condition.DBSyncReadyCondition, condition.InitReason, condition.DBSyncReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateRabbitMqTransportURLReadyCondition, condition.InitReason, designatev1beta1.DesignateRabbitMqTransportURLReadyInitMessage),
			condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
			condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateAPIReadyCondition, condition.InitReason, designatev1beta1.DesignateAPIReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateCentralReadyCondition, condition.InitReason, designatev1beta1.DesignateCentralReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateWorkerReadyCondition, condition.InitReason, designatev1beta1.DesignateWorkerReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateMdnsReadyCondition, condition.InitReason, designatev1beta1.DesignateMdnsReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateProducerReadyCondition, condition.InitReason, designatev1beta1.DesignateProducerReadyInitMessage),
			condition.UnknownCondition(designatev1beta1.DesignateAgentReadyCondition, condition.InitReason, designatev1beta1.DesignateAgentReadyInitMessage),
			condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
			condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
			// service account, role, rolebinding conditions
			condition.UnknownCondition(condition.ServiceAccountReadyCondition, condition.InitReason, condition.ServiceAccountReadyInitMessage),
			condition.UnknownCondition(condition.RoleReadyCondition, condition.InitReason, condition.RoleReadyInitMessage),
			condition.UnknownCondition(condition.RoleBindingReadyCondition, condition.InitReason, condition.RoleBindingReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		return ctrl.Result{}, nil
	}
	if instance.Status.Hash == nil {
		instance.Status.Hash = map[string]string{}
	}
	if instance.Status.APIEndpoints == nil {
		instance.Status.APIEndpoints = map[string]map[string]string{}
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
	}

	// Handle non-deleted clusters
	return r.reconcileNormal(ctx, instance, helper)
}

// SetupWithManager sets up the controller with the Manager.
func (r *DesignateReconciler) SetupWithManager(mgr ctrl.Manager) error {
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
	transportURLSecretFn := func(o client.Object) []reconcile.Request {
		result := []reconcile.Request{}

		// get all Designate CRs
		designates := &designatev1beta1.DesignateList{}
		listOpts := []client.ListOption{
			client.InNamespace(o.GetNamespace()),
		}
		if err := r.Client.List(context.Background(), designates, listOpts...); err != nil {
			r.Log.Error(err, "Unable to retrieve Designate CRs %v")
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
						r.Log.Info(fmt.Sprintf("TransportURL Secret %s belongs to TransportURL belonging to Designate CR %s", o.GetName(), cr.Name))
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

	return ctrl.NewControllerManagedBy(mgr).
		For(&designatev1beta1.Designate{}).
		Owns(&mariadbv1.MariaDBDatabase{}).
		Owns(&designatev1beta1.DesignateAPI{}).
		Owns(&designatev1beta1.DesignateCentral{}).
		Owns(&designatev1beta1.DesignateWorker{}).
		Owns(&designatev1beta1.DesignateMdns{}).
		Owns(&designatev1beta1.DesignateProducer{}).
		Owns(&designatev1beta1.DesignateAgent{}).
		Owns(&rabbitmqv1.TransportURL{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		// Watch for TransportURL Secrets which belong to any TransportURLs created by Designate CRs
		Watches(&source.Kind{Type: &corev1.Secret{}},
			handler.EnqueueRequestsFromMapFunc(transportURLSecretFn)).
		Complete(r)
}

func (r *DesignateReconciler) reconcileDelete(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	r.Log.Info(fmt.Sprintf("Reconciling Service '%s' delete", instance.Name))

	// remove db finalizer first
	db, err := database.GetDatabaseByName(ctx, helper, instance.Name)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !k8s_errors.IsNotFound(err) {
		if err := db.DeleteFinalizer(ctx, helper); err != nil {
			return ctrl.Result{}, err
		}
	}

	// TODO: We might need to control how the sub-services (API, Backup, Scheduler and Volumes) are
	// deleted (when their parent Designate CR is deleted) once we further develop their functionality

	// Service is deleted so remove the finalizer.
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	r.Log.Info(fmt.Sprintf("Reconciled Service '%s' delete successfully", instance.Name))

	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileInit(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
	serviceLabels map[string]string,
	serviceAnnotations map[string]string,
) (ctrl.Result, error) {
	r.Log.Info(fmt.Sprintf("Reconciling Service '%s' init", instance.Name))

	//
	// create service DB instance
	//
	db := database.NewDatabase(
		instance.Name,
		instance.Spec.DatabaseUser,
		instance.Spec.Secret,
		map[string]string{
			"dbName": instance.Spec.DatabaseInstance,
		},
	)
	// create or patch the DB
	ctrlResult, err := db.CreateOrPatchDB(
		ctx,
		helper,
	)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DBReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DBReadyRunningMessage))
		return ctrlResult, nil
	}
	// wait for the DB to be setup
	ctrlResult, err = db.WaitForDBCreated(ctx, helper)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.DBReadyErrorMessage,
			err.Error()))
		return ctrlResult, err
	}
	if (ctrlResult != ctrl.Result{}) {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.DBReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			condition.DBReadyRunningMessage))
		return ctrlResult, nil
	}
	// update Status.DatabaseHostname, used to config the service
	instance.Status.DatabaseHostname = db.GetDatabaseHostname()
	instance.Status.Conditions.MarkTrue(condition.DBReadyCondition, condition.DBReadyMessage)
	// create service DB - end

	//
	// run Designate db sync
	//
	dbSyncHash := instance.Status.Hash[designatev1beta1.DbSyncHash]
	jobDef := designate.DbSyncJob(instance, serviceLabels)

	dbSyncjob := job.NewJob(
		jobDef,
		designatev1beta1.DbSyncHash,
		instance.Spec.PreserveJobs,
		time.Duration(5)*time.Second,
		dbSyncHash,
	)
	ctrlResult, err = dbSyncjob.DoJob(
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
		r.Log.Info(fmt.Sprintf("Service '%s' - Job %s hash added - %s", instance.Name, jobDef.Name, instance.Status.Hash[designatev1beta1.DbSyncHash]))
	}
	instance.Status.Conditions.MarkTrue(condition.DBSyncReadyCondition, condition.DBSyncReadyMessage)

	// when job passed, mark NetworkAttachmentsReadyCondition ready
	instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)

	// run Designate db sync - end

	r.Log.Info(fmt.Sprintf("Reconciled Service '%s' init successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileNormal(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	r.Log.Info(fmt.Sprintf("Reconciling Service '%s'", instance.Name))

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

	serviceLabels := map[string]string{
		common.AppSelector: designate.ServiceName,
	}

	// ConfigMap
	configMapVars := make(map[string]env.Setter)

	//
	// create RabbitMQ transportURL CR and get the actual URL from the associated secret that is created
	//

	transportURL, op, err := r.transportURLCreateOrUpdate(ctx, instance, serviceLabels)

	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateRabbitMqTransportURLReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateRabbitMqTransportURLReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}

	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("TransportURL %s successfully reconciled - operation: %s", transportURL.Name, string(op)))
	}

	instance.Status.TransportURLSecret = transportURL.Status.SecretName

	if instance.Status.TransportURLSecret == "" {
		r.Log.Info(fmt.Sprintf("Waiting for TransportURL %s secret to be created", transportURL.Name))
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateRabbitMqTransportURLReadyCondition,
			condition.RequestedReason,
			condition.SeverityInfo,
			designatev1beta1.DesignateRabbitMqTransportURLReadyRunningMessage))
		return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, nil
	}

	instance.Status.Conditions.MarkTrue(designatev1beta1.DesignateRabbitMqTransportURLReadyCondition, designatev1beta1.DesignateRabbitMqTransportURLReadyMessage)

	// end transportURL

	//
	// check for required OpenStack secret holding passwords for service/admin user and add hash to the vars map
	//
	ospSecret, hash, err := secret.GetSecret(ctx, helper, instance.Spec.Secret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.InputReadyCondition,
				condition.RequestedReason,
				condition.SeverityInfo,
				condition.InputReadyWaitingMessage))
			return ctrl.Result{RequeueAfter: time.Duration(10) * time.Second}, fmt.Errorf("OpenStack secret %s not found", instance.Spec.Secret)
		}
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	// Add a prefix to the var name to avoid accidental collision with other non-secret vars.
	configMapVars["secret-"+ospSecret.Name] = env.SetValue(hash)

	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)
	// run check OpenStack secret - end

	//
	// Create ConfigMaps and Secrets required as input for the Service and calculate an overall hash of hashes
	//

	//
	// create Configmap required for designate input
	// - %-scripts configmap holding scripts to e.g. bootstrap the service
	// - %-config configmap holding minimal designate config required to get the service up, user can add additional files to be added to the service
	// - parameters which has passwords gets added from the OpenStack secret via the init container
	//
	r.Log.Info("pre generateConfigMap ....")

	err = r.generateServiceConfigMaps(ctx, helper, instance, &configMapVars, serviceLabels)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	r.Log.Info("post generateConfigMap ....")

	//
	// create hash over all the different input resources to identify if any those changed
	// and a restart/recreate is required.
	//
	_, hashChanged, err := r.createHashOfInputHashes(ctx, instance, configMapVars)
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
	// Create ConfigMaps and Secrets - end

	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	//
	// TODO check when/if Init, Update, or Upgrade should/could be skipped
	//
	// Note: Dkehn - this will remain in the code base until determination of DNS server connections are determined.
	// networks to attach to
	for _, netAtt := range instance.Spec.DesignateAPI.NetworkAttachments {
		_, err := nad.GetNADWithName(ctx, helper, netAtt, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.NetworkAttachmentsReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					condition.NetworkAttachmentsReadyWaitingMessage,
					netAtt))
				return ctrl.Result{RequeueAfter: time.Second * 10}, fmt.Errorf("network-attachment-definition %s not found", netAtt)
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

	serviceAnnotations, err := nad.CreateNetworksAnnotation(instance.Namespace, instance.Spec.DesignateAPI.NetworkAttachments)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed create network annotation from %s: %w",
			instance.Spec.DesignateAPI.NetworkAttachments, err)
	}

	// Handle service init
	ctrlResult, err := r.reconcileInit(ctx, instance, helper, serviceLabels, serviceAnnotations)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Handle service update
	ctrlResult, err = r.reconcileUpdate(ctx, instance, helper)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	// Handle service upgrade
	ctrlResult, err = r.reconcileUpgrade(ctx, instance, helper)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}

	//
	// normal reconcile tasks
	//
	r.Log.Info("Reconcile tasks starting....")

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
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateAPI status' APIEndpoints and ReadyCount to this parent CR
	instance.Status.APIEndpoints = designateAPI.Status.APIEndpoints
	instance.Status.ServiceIDs = designateAPI.Status.ServiceIDs
	instance.Status.DesignateAPIReadyCount = designateAPI.Status.ReadyCount

	// Mirror DesignateAPI's condition status
	c := designateAPI.Status.Conditions.Mirror(designatev1beta1.DesignateAPIReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment API task reconciled")

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
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateCentral status' ReadyCount to this parent CR
	instance.Status.DesignateCentralReadyCount = designateCentral.Status.ReadyCount

	// Mirror DesignateCentral's condition status
	c = designateCentral.Status.Conditions.Mirror(designatev1beta1.DesignateCentralReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment Central task reconciled")

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
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateWorker status' ReadyCount to this parent CR
	instance.Status.DesignateWorkerReadyCount = designateWorker.Status.ReadyCount

	// Mirror DesignateWorker's condition status
	c = designateWorker.Status.Conditions.Mirror(designatev1beta1.DesignateWorkerReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment Worker task reconciled")

	// deploy designate-mdns
	designateMdns, op, err := r.mdnsDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateMdnsReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateMdnsReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateMdns status' ReadyCount to this parent CR
	instance.Status.DesignateMdnsReadyCount = designateMdns.Status.ReadyCount

	// Mirror DesignateMdns's condition status
	c = designateMdns.Status.Conditions.Mirror(designatev1beta1.DesignateMdnsReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment Mdns task reconciled")

	// deploy designate-agent
	designateAgent, op, err := r.agentDeploymentCreateOrUpdate(ctx, instance)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			designatev1beta1.DesignateAgentReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateAgentReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateAgent status' ReadyCount to this parent CR
	instance.Status.DesignateAgentReadyCount = designateAgent.Status.ReadyCount

	// Mirror DesignateAgent's condition status
	c = designateAgent.Status.Conditions.Mirror(designatev1beta1.DesignateAgentReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment Agent task reconciled")

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
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("Deployment %s successfully reconciled - operation: %s", instance.Name, string(op)))
	}

	// Mirror DesignateProducer status' ReadyCount to this parent CR
	instance.Status.DesignateProducerReadyCount = designateProducer.Status.ReadyCount

	// Mirror DesignateProducer's condition status
	c = designateProducer.Status.Conditions.Mirror(designatev1beta1.DesignateProducerReadyCondition)
	if c != nil {
		instance.Status.Conditions.Set(c)
	}
	r.Log.Info("Deployment Producer task reconciled")

	r.Log.Info(fmt.Sprintf("Reconciled Service '%s' successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileUpdate(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	r.Log.Info(fmt.Sprintf("Reconciling Service '%s' update", instance.Name))

	// TODO: should have minor update tasks if required
	// - delete dbsync hash from status to rerun it?

	r.Log.Info(fmt.Sprintf("Reconciled Service '%s' update successfully", instance.Name))
	return ctrl.Result{}, nil
}

func (r *DesignateReconciler) reconcileUpgrade(ctx context.Context, instance *designatev1beta1.Designate, helper *helper.Helper) (ctrl.Result, error) {
	r.Log.Info(fmt.Sprintf("Reconciling Service '%s' upgrade", instance.Name))

	// TODO: should have major version upgrade tasks
	// -delete dbsync hash from status to rerun it?

	r.Log.Info(fmt.Sprintf("Reconciled Service '%s' upgrade successfully", instance.Name))
	return ctrl.Result{}, nil
}

// generateServiceConfigMaps - create create configmaps which hold scripts and service configuration
// TODO add DefaultConfigOverwrite
func (r *DesignateReconciler) generateServiceConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *designatev1beta1.Designate,
	envVars *map[string]env.Setter,
	serviceLabels map[string]string,
) error {
	//
	// create Configmap/Secret required for designate input
	// - %-scripts configmap holding scripts to e.g. bootstrap the service
	// - %-config configmap holding minimal designate config required to get the service up, user can add additional files to be added to the service
	// - parameters which has passwords gets added from the ospSecret via the init container
	//

	cmLabels := labels.GetLabels(instance, labels.GetGroupLabel(designate.ServiceName), serviceLabels)

	// customData hold any customization for the service.
	// custom.conf is going to /etc/<service>/<service>.conf.d
	// all other files get placed into /etc/<service> to allow overwrite of e.g. policy.json
	// TODO: make sure custom.conf can not be overwritten
	customData := map[string]string{common.CustomServiceConfigFileName: instance.Spec.CustomServiceConfig}

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
	templateParameters := make(map[string]interface{})
	templateParameters["ServiceUser"] = instance.Spec.ServiceUser
	templateParameters["KeystoneInternalURL"] = keystoneInternalURL
	templateParameters["KeystonePublicURL"] = keystonePublicURL

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

	return configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)
}

// createHashOfInputHashes - creates a hash of hashes which gets added to the resources which requires a restart
// if any of the input resources change, like configs, passwords, ...
//
// returns the hash, whether the hash changed (as a bool) and any error
func (r *DesignateReconciler) createHashOfInputHashes(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	envVars map[string]env.Setter,
) (string, bool, error) {
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
	return hash, changed, nil
}

func (r *DesignateReconciler) transportURLCreateOrUpdate(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	serviceLabels map[string]string,
) (*rabbitmqv1.TransportURL, controllerutil.OperationResult, error) {
	transportURL := &rabbitmqv1.TransportURL{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-designate-transport", instance.Name),
			Namespace: instance.Namespace,
			Labels:    serviceLabels,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, transportURL, func() error {
		transportURL.Spec.RabbitmqClusterName = instance.Spec.RabbitMqClusterName

		err := controllerutil.SetControllerReference(instance, transportURL, r.Scheme)
		return err
	})

	return transportURL, op, err
}

func (r *DesignateReconciler) apiDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateAPI, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateAPI{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-api", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateAPI
		// Add in transfers from umbrella Designate (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateCentral
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateWorker
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) mdnsDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateMdns, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateMdns{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-mdns", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateMdns
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) producerDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateProducer, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateProducer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-producer", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateProducer
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) agentDeploymentCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateAgent, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateAgent{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-agent", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateAgent
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseUser = instance.Spec.DatabaseUser
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		if len(deployment.Spec.NodeSelector) == 0 {
			deployment.Spec.NodeSelector = instance.Spec.NodeSelector
		}

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}
