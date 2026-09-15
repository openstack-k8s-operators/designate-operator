/*
Copyright 2026.

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
	"testing"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var errChildUpdateFailed = errors.New("child update failed")

func TestReconcileSubresourceMirrorsObservedChild(t *testing.T) {
	instance := &designatev1beta1.Designate{}
	childConditions := condition.Conditions{}
	childConditions.MarkTrue(designatev1beta1.DesignateAPIReadyCondition, "API is ready")

	r := &DesignateReconciler{}
	result, err := r.reconcileSubresource(context.Background(), instance, designateSubresourceSpec{
		readyCondition: designatev1beta1.DesignateAPIReadyCondition,
		initMessage:    designatev1beta1.DesignateAPIReadyInitMessage,
		errorMessage:   designatev1beta1.DesignateAPIReadyErrorMessage,
		setReadyCount: func(status *designatev1beta1.DesignateStatus, count int32) {
			status.DesignateAPIReadyCount = count
		},
		reconcile: func(
			context.Context,
			*designatev1beta1.Designate,
		) (designateSubresourceStatus, controllerutil.OperationResult, error) {
			return designateSubresourceStatus{
				name:               "api",
				generation:         3,
				observedGeneration: 3,
				readyCount:         2,
				conditions:         childConditions,
			}, controllerutil.OperationResultCreated, nil
		},
	})

	if err != nil {
		t.Fatalf("reconcileSubresource() error = %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("reconcileSubresource() result = %#v, want empty result", result)
	}
	if instance.Status.DesignateAPIReadyCount != 2 {
		t.Errorf("ready count = %d, want 2", instance.Status.DesignateAPIReadyCount)
	}
	got := instance.Status.Conditions.Get(designatev1beta1.DesignateAPIReadyCondition)
	if got == nil || got.Status != corev1.ConditionTrue {
		t.Fatalf("API condition = %#v, want True", got)
	}
}

func TestReconcileSubresourceDoesNotMirrorStaleChild(t *testing.T) {
	instance := &designatev1beta1.Designate{}
	instance.Status.DesignateAPIReadyCount = 7

	r := &DesignateReconciler{}
	result, err := r.reconcileSubresource(context.Background(), instance, designateSubresourceSpec{
		readyCondition: designatev1beta1.DesignateAPIReadyCondition,
		initMessage:    designatev1beta1.DesignateAPIReadyInitMessage,
		errorMessage:   designatev1beta1.DesignateAPIReadyErrorMessage,
		setReadyCount: func(status *designatev1beta1.DesignateStatus, count int32) {
			status.DesignateAPIReadyCount = count
		},
		reconcile: func(
			context.Context,
			*designatev1beta1.Designate,
		) (designateSubresourceStatus, controllerutil.OperationResult, error) {
			return designateSubresourceStatus{
				name:               "api",
				generation:         4,
				observedGeneration: 3,
				readyCount:         1,
			}, controllerutil.OperationResultNone, nil
		},
	})

	if err != nil {
		t.Fatalf("reconcileSubresource() error = %v", err)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("reconcileSubresource() result = %#v, want empty result", result)
	}
	if instance.Status.DesignateAPIReadyCount != 7 {
		t.Errorf("ready count = %d, want stale value 7", instance.Status.DesignateAPIReadyCount)
	}
	got := instance.Status.Conditions.Get(designatev1beta1.DesignateAPIReadyCondition)
	if got == nil || got.Status != corev1.ConditionUnknown {
		t.Fatalf("API condition = %#v, want Unknown", got)
	}
}

func TestReconcileSubresourceReturnsChildError(t *testing.T) {
	instance := &designatev1beta1.Designate{}
	wantErr := errChildUpdateFailed

	r := &DesignateReconciler{}
	result, err := r.reconcileSubresource(context.Background(), instance, designateSubresourceSpec{
		readyCondition: designatev1beta1.DesignateAPIReadyCondition,
		initMessage:    designatev1beta1.DesignateAPIReadyInitMessage,
		errorMessage:   designatev1beta1.DesignateAPIReadyErrorMessage,
		setReadyCount: func(status *designatev1beta1.DesignateStatus, count int32) {
			status.DesignateAPIReadyCount = count
		},
		reconcile: func(
			context.Context,
			*designatev1beta1.Designate,
		) (designateSubresourceStatus, controllerutil.OperationResult, error) {
			return designateSubresourceStatus{}, controllerutil.OperationResultNone, wantErr
		},
	})

	if !errors.Is(err, wantErr) {
		t.Fatalf("reconcileSubresource() error = %v, want %v", err, wantErr)
	}
	if result != (ctrl.Result{}) {
		t.Fatalf("reconcileSubresource() result = %#v, want empty result", result)
	}
	got := instance.Status.Conditions.Get(designatev1beta1.DesignateAPIReadyCondition)
	if got == nil || got.Status != corev1.ConditionFalse {
		t.Fatalf("API condition = %#v, want False", got)
	}
}
