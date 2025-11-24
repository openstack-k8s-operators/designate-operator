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

// Package v1beta1 implements webhook handlers for Designate v1beta1 API resources.
package v1beta1

import (
	"context"
	"errors"
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
)

var (
	// ErrInvalidObjectType is returned when an unexpected object type is provided
	ErrInvalidObjectType = errors.New("invalid object type")
)

// nolint:unused
// log is for logging in this package.
var designatelog = logf.Log.WithName("designate-resource")

// SetupDesignateWebhookWithManager registers the webhook for Designate in the manager.
func SetupDesignateWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).For(&designatev1beta1.Designate{}).
		WithValidator(&DesignateCustomValidator{}).
		WithDefaulter(&DesignateCustomDefaulter{}).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-designate-openstack-org-v1beta1-designate,mutating=true,failurePolicy=fail,sideEffects=None,groups=designate.openstack.org,resources=designates,verbs=create;update,versions=v1beta1,name=mdesignate-v1beta1.kb.io,admissionReviewVersions=v1

// DesignateCustomDefaulter struct is responsible for setting default values on the custom resource of the
// Kind Designate when those are created or updated.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as it is used only for temporary operations and does not need to be deeply copied.
type DesignateCustomDefaulter struct {
	// TODO(user): Add more fields as needed for defaulting
}

var _ webhook.CustomDefaulter = &DesignateCustomDefaulter{}

// Default implements webhook.CustomDefaulter so a webhook will be registered for the Kind Designate.
func (d *DesignateCustomDefaulter) Default(_ context.Context, obj runtime.Object) error {
	designate, ok := obj.(*designatev1beta1.Designate)

	if !ok {
		return fmt.Errorf("expected an Designate object but got %T: %w", obj, ErrInvalidObjectType)
	}
	designatelog.Info("Defaulting for Designate", "name", designate.GetName())

	// Call the Default method on the Designate type
	designate.Default()

	return nil
}

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: The 'path' attribute must follow a specific pattern and should not be modified directly here.
// Modifying the path for an invalid path can cause API server errors; failing to locate the webhook.
// +kubebuilder:webhook:path=/validate-designate-openstack-org-v1beta1-designate,mutating=false,failurePolicy=fail,sideEffects=None,groups=designate.openstack.org,resources=designates,verbs=create;update,versions=v1beta1,name=vdesignate-v1beta1.kb.io,admissionReviewVersions=v1

// DesignateCustomValidator struct is responsible for validating the Designate resource
// when it is created, updated, or deleted.
//
// NOTE: The +kubebuilder:object:generate=false marker prevents controller-gen from generating DeepCopy methods,
// as this struct is used only for temporary operations and does not need to be deeply copied.
type DesignateCustomValidator struct {
	// TODO(user): Add more fields as needed for validation
}

var _ webhook.CustomValidator = &DesignateCustomValidator{}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type Designate.
func (v *DesignateCustomValidator) ValidateCreate(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	designate, ok := obj.(*designatev1beta1.Designate)
	if !ok {
		return nil, fmt.Errorf("expected a Designate object but got %T: %w", obj, ErrInvalidObjectType)
	}
	designatelog.Info("Validation for Designate upon creation", "name", designate.GetName())

	// Call the ValidateCreate method on the Designate type
	return designate.ValidateCreate()
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type Designate.
func (v *DesignateCustomValidator) ValidateUpdate(_ context.Context, oldObj, newObj runtime.Object) (admission.Warnings, error) {
	designate, ok := newObj.(*designatev1beta1.Designate)
	if !ok {
		return nil, fmt.Errorf("expected a Designate object for the newObj but got %T: %w", newObj, ErrInvalidObjectType)
	}
	designatelog.Info("Validation for Designate upon update", "name", designate.GetName())

	// Call the ValidateUpdate method on the Designate type
	return designate.ValidateUpdate(oldObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type Designate.
func (v *DesignateCustomValidator) ValidateDelete(_ context.Context, obj runtime.Object) (admission.Warnings, error) {
	designate, ok := obj.(*designatev1beta1.Designate)
	if !ok {
		return nil, fmt.Errorf("expected a Designate object but got %T: %w", obj, ErrInvalidObjectType)
	}
	designatelog.Info("Validation for Designate upon deletion", "name", designate.GetName())

	// Call the ValidateDelete method on the Designate type
	return designate.ValidateDelete()
}
