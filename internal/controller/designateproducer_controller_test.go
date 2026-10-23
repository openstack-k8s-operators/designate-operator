/*
Copyright 2026 Red Hat

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
	"path/filepath"
	"testing"

	"github.com/go-logr/logr"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestProducerWaitsForRequiredSecrets(t *testing.T) {
	t.Setenv("OPERATOR_TEMPLATES", filepath.Join("..", "..", "templates"))
	inputs := []string{"passwords", "transport", "designate-scripts", "designate-config-data", "designate-defaults"}
	for _, missing := range inputs {
		t.Run(missing, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			if err := corev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			if err := designatev1.AddToScheme(scheme); err != nil {
				t.Fatal(err)
			}
			instance := &designatev1.DesignateProducer{
				TypeMeta: metav1.TypeMeta{APIVersion: designatev1.GroupVersion.String(), Kind: "DesignateProducer"},
				ObjectMeta: metav1.ObjectMeta{
					Name: "producer", Namespace: "test", UID: "producer-uid",
					OwnerReferences: []metav1.OwnerReference{{APIVersion: designatev1.GroupVersion.String(), Kind: "Designate", Name: "designate"}},
				},
			}
			instance.Spec.Secret = "passwords"
			instance.Spec.TransportURLSecret = "transport"
			objects := []client.Object{instance}
			for _, name := range inputs {
				if name != missing {
					objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: instance.Namespace}, Data: map[string][]byte{"fixture": []byte("value")}})
				}
			}
			// Stop at any attempted resource creation. Missing inputs must return
			// before configuration rendering or deployment reconciliation starts.
			created := false
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
					created = true
					return k8s_errors.NewBadRequest("unexpected resource creation while inputs are missing")
				},
			}).Build()
			r := &DesignateProducerReconciler{Client: c, Scheme: scheme}
			h, err := helper.NewHelper(instance, c, nil, scheme, logr.Discard())
			if err != nil {
				t.Fatal(err)
			}
			result, err := r.reconcileNormal(ctx, instance, h)
			if err != nil || result.RequeueAfter == 0 || created {
				t.Fatalf("missing %s must requeue without creating resources: result=%+v err=%v created=%t", missing, result, err, created)
			}
			if !instance.Status.Conditions.IsFalse(condition.InputReadyCondition) {
				t.Fatal("missing input was not reported as InputReady=False")
			}
		})
	}
}
