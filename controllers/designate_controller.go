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
	"sort"
	"time"

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
	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
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
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;create;update;patch;delete;watch

// service account, role, rolebinding
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch
// service account permissions that are needed to grant permission to the above
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid;privileged,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

// Reconcile -
func (r *DesignateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	Log := r.GetLogger(ctx)

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
	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
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
		condition.UnknownCondition(designatev1beta1.DesignateBackendbind9ReadyCondition, condition.InitReason, designatev1beta1.DesignateBackendbind9ReadyInitMessage),
		condition.UnknownCondition(condition.NetworkAttachmentsReadyCondition, condition.InitReason, condition.NetworkAttachmentsReadyInitMessage),
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
	caBundleSecretNameField = ".spec.tls.caBundleSecretName"
	tlsAPIInternalField     = ".spec.tls.api.internal.secretName"
	tlsAPIPublicField       = ".spec.tls.api.public.secretName"
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
		if err := r.Client.List(context.Background(), designates, listOpts...); err != nil {
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
		Owns(&rabbitmqv1.TransportURL{}).
		Owns(&redisv1.Redis{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		// Watch for TransportURL Secrets which belong to any TransportURLs created by Designate CRs
		Watches(&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(transportURLSecretFn)).
		Complete(r)
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
		// Hash changed and instance status should be updated (which will be done by main defer func),
		// so we need to return and reconcile again
		Log.Info("input hashes have changed, restarting reconcile")
		return ctrl.Result{}, nil
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
			designatev1beta1.DesignateRabbitMqTransportURLReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			designatev1beta1.DesignateRabbitMqTransportURLReadyErrorMessage,
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

	instance.Status.Conditions.MarkTrue(designatev1beta1.DesignateRabbitMqTransportURLReadyCondition, designatev1beta1.DesignateRabbitMqTransportURLReadyMessage)

	// end transportURL

	redis, op, err := r.redisCreateOrUpdate(ctx, instance, helper)
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		Log.Info(fmt.Sprintf("Redis %s successfully reconciled - operation: %s", redis.Name, string(op)))
	}

	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	//
	// TODO check when/if Init, Update, or Upgrade should/could be skipped
	//

	serviceLabels := map[string]string{
		common.AppSelector: designate.ServiceName,
	}

	// Note: Dkehn - this will remain in the code base until determination of DNS server connections are determined.
	// networks to attach to
	nadList := []networkv1.NetworkAttachmentDefinition{}
	for _, netAtt := range instance.Spec.DesignateAPI.NetworkAttachments {
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
			instance.Spec.DesignateAPI.NetworkAttachments, err)
	}

	instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)

	// Handle service init
	ctrlResult, err := r.reconcileInit(ctx, instance, helper, serviceLabels, serviceAnnotations)
	if err != nil {
		return ctrlResult, err
	} else if (ctrlResult != ctrl.Result{}) {
		return ctrlResult, nil
	}
	instance.Status.Conditions.MarkTrue(condition.NetworkAttachmentsReadyCondition, condition.NetworkAttachmentsReadyMessage)

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
	mdnsLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.ObjectMeta.Name), map[string]string{})
	mdnsConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.MdnsPredIPConfigMap, mdnsLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	bindLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.ObjectMeta.Name), map[string]string{})
	bindConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.BindPredIPConfigMap, bindLabels)
	if err != nil {
		return ctrl.Result{}, err
	}

	nsRecordsLabels := labels.GetLabels(instance, labels.GetGroupLabel(instance.ObjectMeta.Name), map[string]string{})
	nsRecordsConfigMap, err := r.handleConfigMap(ctx, helper, instance, designate.NsRecordsConfigMap, nsRecordsLabels)
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

	// Get a list of the nodes in the cluster

	// TODO(oschwart):
	// * confirm whether or not this lists only the nodes we want (i.e. ones
	// that will host the daemonset)
	// * do we want to provide a mechanism to temporarily disabling this list
	// for maintenance windows where nodes might be "coming and going"

	nodes, err := helper.GetKClient().CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return ctrl.Result{}, err
	}

	var nodeNames []string
	for _, node := range nodes.Items {
		nodeNames = append(nodeNames, fmt.Sprintf("mdns_%s", node.Name))
	}

	updatedMap, allocatedIPs, err := r.allocatePredictableIPs(ctx, predictableIPParams, nodeNames, mdnsConfigMap.Data, allocatedIPs)
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
	bindReplicaCount := int(*instance.Spec.DesignateBackendbind9.Replicas)
	var bindNames []string
	for i := 0; i < bindReplicaCount; i++ {
		bindNames = append(bindNames, fmt.Sprintf("bind_address_%d", i))
	}

	updatedBindMap, _, err := r.allocatePredictableIPs(ctx, predictableIPParams, bindNames, bindConfigMap.Data, allocatedIPs)
	if err != nil {
		return ctrl.Result{}, err
	}

	_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), bindConfigMap, func() error {
		bindConfigMap.Labels = util.MergeStringMaps(bindConfigMap.Labels, bindLabels)
		bindConfigMap.Data = updatedBindMap
		return controllerutil.SetControllerReference(instance, bindConfigMap, helper.GetScheme())
	})

	if err != nil {
		Log.Info("Unable to create config map for bind ips...")
		return ctrl.Result{}, err
	}

	if err != nil {
		return ctrl.Result{}, err
	}
	if len(nsRecordsConfigMap.Data) > 0 {
		poolsYamlConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      designate.PoolsYamlsConfigMap,
				Namespace: instance.GetNamespace(),
				Labels:    bindLabels,
			},
			Data: make(map[string]string),
		}
		poolsYaml, err := designate.GeneratePoolsYamlData(bindConfigMap.Data, mdnsConfigMap.Data, nsRecordsConfigMap.Data)
		if err != nil {
			return ctrl.Result{}, err
		}
		Log.Info(fmt.Sprintf("pools.yaml content is\n%v", poolsYaml))
		updatedPoolsYaml := make(map[string]string)
		updatedPoolsYaml[designate.PoolsYamlsConfigMap] = poolsYaml

		_, err = controllerutil.CreateOrPatch(ctx, helper.GetClient(), poolsYamlConfigMap, func() error {
			poolsYamlConfigMap.Labels = util.MergeStringMaps(poolsYamlConfigMap.Labels, bindLabels)
			poolsYamlConfigMap.Data = updatedPoolsYaml
			return controllerutil.SetControllerReference(instance, poolsYamlConfigMap, helper.GetScheme())
		})
		if err != nil {
			Log.Info("Unable to create config map for pools.yaml file")
			return ctrl.Result{}, err
		}
		configMaps := []interface{}{
			poolsYamlConfigMap.Data,
		}

		poolsYamlsEnvVars := make(map[string]env.Setter)
		_, changed, err := r.createHashOfInputHashes(ctx, instance, designate.PoolsYamlHash, poolsYamlsEnvVars, configMaps)
		if err != nil {
			return ctrl.Result{}, err
		} else if changed {
			// launch the pool update job
			Log.Info("Creating a pool update job")
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
	designateMdns, op, err := r.mdnsDaemonSetCreateOrUpdate(ctx, instance)
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
	designateUnbound, op, err := r.unboundDeploymentCreateOrUpdate(ctx, instance)
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
// TODO add DefaultConfigOverwrite
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
	replicas := int(*instance.Spec.DesignateBackendbind9.Replicas)

	// Get the secret first by providing the same name and namespace
	secret := &corev1.Secret{}
	err := h.GetClient().Get(ctx, types.NamespacedName{
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

		for i := 0; i < replicas; i++ {
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
	if instance.Spec.DesignateAPI.TLS.Ca.CaBundleSecretName != "" {
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

	for key, data := range instance.Spec.DefaultConfigOverwrite {
		customData[key] = data
	}

	databaseAccount := designateDb.GetAccount()
	dbSecret := designateDb.GetSecret()

	// We only need a minimal 00-config.conf that is only used by db-sync job,
	// hence only passing the database related parameters
	templateParameters := map[string]interface{}{
		"MinimalConfig": true, // This tells the template to generate a minimal config
		"DatabaseConnection": fmt.Sprintf("mysql+pymysql://%s:%s@%s/%s?read_default_file=/etc/my.cnf",
			databaseAccount.Spec.UserName,
			string(dbSecret.Data[mariadbv1.DatabasePasswordSelector]),
			instance.Status.DatabaseHostname,
			designate.DatabaseName,
		),
	}
	templateParameters["ServiceUser"] = instance.Spec.ServiceUser

	transportURLSecret, _, err := oko_secret.GetSecret(ctx, h, instance.Status.TransportURLSecret, instance.Namespace)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			Log.Info(fmt.Sprintf("TransportURL secret %s not found", instance.Status.TransportURLSecret))
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
	additionalConfigmaps []interface{},
) (string, bool, error) {
	Log := r.GetLogger(ctx)

	var hashMap map[string]string
	changed := false
	mergedMapVars := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	combinedHashes := []string{}

	envHash, err := util.ObjectHash(mergedMapVars)
	if err != nil {
		Log.Info("XXX - Error creating hash")
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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateAPI
		// Add in transfers from umbrella Designate (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseAccount = instance.Spec.DatabaseAccount
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.NodeSelector = instance.Spec.DesignateAPI.NodeSelector

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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateCentral
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseAccount = instance.Spec.DatabaseAccount
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.RedisHostIPs = instance.Status.RedisHostIPs
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateCentral.NodeSelector

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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateWorker
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseAccount = instance.Spec.DatabaseAccount
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateWorker.NodeSelector

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
}

func (r *DesignateReconciler) mdnsDaemonSetCreateOrUpdate(ctx context.Context, instance *designatev1beta1.Designate) (*designatev1beta1.DesignateMdns, controllerutil.OperationResult, error) {
	daemonset := &designatev1beta1.DesignateMdns{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-mdns", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateMdns.NodeSelector == nil {
		instance.Spec.DesignateMdns.NodeSelector = instance.Spec.NodeSelector
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, daemonset, func() error {
		daemonset.Spec = instance.Spec.DesignateMdns
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		daemonset.Spec.ServiceUser = instance.Spec.ServiceUser
		daemonset.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		daemonset.Spec.DatabaseAccount = instance.Spec.DatabaseAccount
		daemonset.Spec.Secret = instance.Spec.Secret
		daemonset.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		daemonset.Spec.ServiceAccount = instance.RbacResourceName()
		daemonset.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		daemonset.Spec.NodeSelector = instance.Spec.DesignateMdns.NodeSelector

		err := controllerutil.SetControllerReference(instance, daemonset, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return daemonset, op, err
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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateProducer
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceUser = instance.Spec.ServiceUser
		deployment.Spec.DatabaseHostname = instance.Status.DatabaseHostname
		deployment.Spec.DatabaseAccount = instance.Spec.DatabaseAccount
		deployment.Spec.Secret = instance.Spec.Secret
		deployment.Spec.TransportURLSecret = instance.Status.TransportURLSecret
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.RedisHostIPs = instance.Status.RedisHostIPs
		deployment.Spec.TLS = instance.Spec.DesignateAPI.TLS.Ca
		deployment.Spec.NodeSelector = instance.Spec.DesignateProducer.NodeSelector

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

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, statefulSet, func() error {
		statefulSet.Spec = instance.Spec.DesignateBackendbind9
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		statefulSet.Spec.ServiceUser = instance.Spec.ServiceUser
		statefulSet.Spec.Secret = instance.Spec.Secret
		statefulSet.Spec.PasswordSelectors = instance.Spec.PasswordSelectors
		statefulSet.Spec.ServiceAccount = instance.RbacResourceName()
		statefulSet.Spec.NodeSelector = instance.Spec.DesignateBackendbind9.NodeSelector

		err := controllerutil.SetControllerReference(instance, statefulSet, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return statefulSet, op, err
}

func (r *DesignateReconciler) unboundDeploymentCreateOrUpdate(
	ctx context.Context,
	instance *designatev1beta1.Designate,
) (*designatev1beta1.DesignateUnbound, controllerutil.OperationResult, error) {
	deployment := &designatev1beta1.DesignateUnbound{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-unbound", instance.Name),
			Namespace: instance.Namespace,
		},
	}

	if instance.Spec.DesignateUnbound.NodeSelector == nil {
		instance.Spec.DesignateUnbound.NodeSelector = instance.Spec.NodeSelector
	}

	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		deployment.Spec = instance.Spec.DesignateUnbound
		// Add in transfers from umbrella Designate CR (this instance) spec
		// TODO: Add logic to determine when to set/overwrite, etc
		deployment.Spec.ServiceAccount = instance.RbacResourceName()
		deployment.Spec.NodeSelector = instance.Spec.DesignateUnbound.NodeSelector

		err := controllerutil.SetControllerReference(instance, deployment, r.Scheme)
		if err != nil {
			return err
		}

		return nil
	})

	return deployment, op, err
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
	if err := r.Client.List(context.Background(), api, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), central, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), worker, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), mdns, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), prd, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), prd, listOpts...); err != nil {
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
	if err := r.Client.List(context.Background(), prd, listOpts...); err != nil {
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
	redis *redisv1.Redis,
) ([]string, error) {
	getOptions := metav1.GetOptions{}
	service, err := helper.GetKClient().CoreV1().Services(instance.Namespace).Get(ctx, "redis", getOptions)
	if err != nil {
		return []string{}, err
	}
	// TODO Ensure that the correct port is exposed
	return service.Spec.ClusterIPs, nil
}

func (r *DesignateReconciler) redisCreateOrUpdate(
	ctx context.Context,
	instance *designatev1beta1.Designate,
	helper *helper.Helper,
) (*redisv1.Redis, controllerutil.OperationResult, error) {
	redis := &redisv1.Redis{
		// Use the "global" redis instance.
		ObjectMeta: metav1.ObjectMeta{
			Name:      "redis",
			Namespace: instance.Namespace,
		},
	}

	op, err := controllerutil.CreateOrUpdate(context.TODO(), r.Client, redis, func() error {
		// We probably don't want to own the redis instance.
		//err := controllerutil.SetControllerReference(instance, redis, r.Scheme)
		//return err
		return nil
	})
	if err != nil {
		return nil, op, err
	}

	hostIPs, err := getRedisServiceIPs(ctx, instance, helper, redis)
	if err != nil {
		return redis, op, err
	}

	sort.Strings(hostIPs)
	instance.Status.RedisHostIPs = hostIPs

	return redis, op, err
}
