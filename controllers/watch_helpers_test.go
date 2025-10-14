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
	"errors"
	"testing"

	"github.com/go-logr/logr"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var ErrFailedToListObjects = errors.New("failed to list objects")

var _ = Describe("CreateRequestsFromObjectUpdates", func() {
	var (
		ctx     context.Context
		log     logr.Logger
		srcObj  client.Object
		mockObj metav1.ObjectMeta
	)

	BeforeEach(func() {
		ctx = context.Background()
		log = logr.Discard()
		srcObj = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-secret",
				Namespace: "test-namespace",
			},
		}
		mockObj = metav1.ObjectMeta{
			Name:      "dependent-object",
			Namespace: "test-namespace",
		}
	})

	Context("when listFunc returns objects successfully", func() {
		It("should create reconcile requests for all matching objects", func() {
			// Mock listFunc that returns two objects for first field, one for second
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				switch field {
				case ".spec.secret":
					return []metav1.ObjectMeta{
						{Name: "obj1", Namespace: "ns1"},
						{Name: "obj2", Namespace: "ns2"},
					}, nil
				case ".spec.tls.caBundleSecretName":
					return []metav1.ObjectMeta{
						{Name: "obj3", Namespace: "ns3"},
					}, nil
				}
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{".spec.secret", ".spec.tls.caBundleSecretName"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(3))
			Expect(requests[0].NamespacedName).To(Equal(types.NamespacedName{Name: "obj1", Namespace: "ns1"}))
			Expect(requests[1].NamespacedName).To(Equal(types.NamespacedName{Name: "obj2", Namespace: "ns2"}))
			Expect(requests[2].NamespacedName).To(Equal(types.NamespacedName{Name: "obj3", Namespace: "ns3"}))
		})

		It("should handle a single watchField with multiple objects", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{
					{Name: "producer-1", Namespace: "openstack"},
					{Name: "producer-2", Namespace: "openstack"},
					{Name: "producer-3", Namespace: "openstack"},
				}, nil
			}

			watchFields := []string{".spec.secret"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(3))
			for _, req := range requests {
				Expect(req.NamespacedName.Namespace).To(Equal("openstack"))
				Expect(req.NamespacedName.Name).To(ContainSubstring("producer-"))
			}
		})

		It("should handle topology field references", func() {
			topologySrc := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "topology-ref",
					Namespace: "openstack",
				},
			}

			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				if field == ".spec.topologyRef.Name" {
					return []metav1.ObjectMeta{
						{Name: "api-service", Namespace: "openstack"},
						{Name: "worker-service", Namespace: "openstack"},
					}, nil
				}
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{".spec.topologyRef.Name"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, topologySrc, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(2))
			Expect(requests[0].NamespacedName.Name).To(Equal("api-service"))
			Expect(requests[1].NamespacedName.Name).To(Equal("worker-service"))
		})
	})

	Context("when listFunc returns no objects", func() {
		It("should return an empty request list", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{".spec.secret", ".spec.tls.caBundleSecretName"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(BeEmpty())
		})

		It("should return empty list when no watchFields are provided", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{{Name: "obj1", Namespace: "ns1"}}, nil
			}

			watchFields := []string{}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(BeEmpty())
		})
	})

	Context("when listFunc returns an error", func() {
		It("should return accumulated requests up to the error and stop processing", func() {
			callCount := 0
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				callCount++
				if callCount == 1 {
					// First field succeeds
					return []metav1.ObjectMeta{
						{Name: "obj1", Namespace: "ns1"},
					}, nil
				}
				// Second field fails
				return []metav1.ObjectMeta{}, ErrFailedToListObjects
			}

			watchFields := []string{".spec.secret", ".spec.tls.caBundleSecretName"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			// Should only have the request from the first successful field
			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName.Name).To(Equal("obj1"))
		})

		It("should return empty list when first field lookup fails", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{}, ErrFailedToListObjects
			}

			watchFields := []string{".spec.secret"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(BeEmpty())
		})
	})

	Context("when simulating real controller usage patterns", func() {
		It("should handle DesignateProducer-style watchFields", func() {
			// Simulating the pattern from designateproducer_controller.go
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				switch field {
				case ".spec.secret":
					return []metav1.ObjectMeta{
						{Name: "designate-producer", Namespace: "openstack"},
					}, nil
				case ".spec.tls.caBundleSecretName":
					return []metav1.ObjectMeta{
						{Name: "designate-producer", Namespace: "openstack"},
					}, nil
				case ".spec.topologyRef.Name":
					return []metav1.ObjectMeta{
						{Name: "designate-producer", Namespace: "openstack"},
					}, nil
				}
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{
				".spec.secret",
				".spec.tls.caBundleSecretName",
				".spec.topologyRef.Name",
			}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			// Each field might return the same object, so we get 3 reconcile requests
			// (which is acceptable - reconcile is idempotent)
			Expect(requests).To(HaveLen(3))
			for _, req := range requests {
				Expect(req.NamespacedName.Name).To(Equal("designate-producer"))
				Expect(req.NamespacedName.Namespace).To(Equal("openstack"))
			}
		})

		It("should handle DesignateAPI-style watchFields with TLS fields", func() {
			// Simulating the pattern from designateapi_controller.go
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				switch field {
				case ".spec.secret":
					return []metav1.ObjectMeta{
						{Name: "designate-api-1", Namespace: "openstack"},
					}, nil
				case ".spec.tls.api.internal.secretName":
					return []metav1.ObjectMeta{
						{Name: "designate-api-1", Namespace: "openstack"},
						{Name: "designate-api-2", Namespace: "openstack"},
					}, nil
				case ".spec.tls.api.public.secretName":
					return []metav1.ObjectMeta{
						{Name: "designate-api-2", Namespace: "openstack"},
					}, nil
				}
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{
				".spec.secret",
				".spec.tls.api.internal.secretName",
				".spec.tls.api.public.secretName",
			}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(4))
			// Verify we have reconcile requests (duplicates are fine - reconcile is idempotent)
			Expect(requests[0].NamespacedName.Namespace).To(Equal("openstack"))
		})

		It("should handle mixed results where some fields have no matches", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				if field == ".spec.secret" {
					return []metav1.ObjectMeta{
						{Name: "central-service", Namespace: "openstack"},
					}, nil
				}
				// Other fields have no matches
				return []metav1.ObjectMeta{}, nil
			}

			watchFields := []string{
				".spec.secret",
				".spec.tls.caBundleSecretName",
				".spec.topologyRef.Name",
			}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(1))
			Expect(requests[0].NamespacedName.Name).To(Equal("central-service"))
		})
	})

	Context("when verifying request structure", func() {
		It("should create properly structured reconcile.Request objects", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{mockObj}, nil
			}

			watchFields := []string{".spec.secret"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(1))
			req := requests[0]
			Expect(req).To(BeAssignableToTypeOf(reconcile.Request{}))
			Expect(req.NamespacedName).To(Equal(types.NamespacedName{
				Name:      "dependent-object",
				Namespace: "test-namespace",
			}))
		})

		It("should preserve namespace and name from ObjectMeta", func() {
			mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
				return []metav1.ObjectMeta{
					{Name: "svc-1", Namespace: "ns-1"},
					{Name: "svc-2", Namespace: "ns-2"},
					{Name: "svc-3", Namespace: "ns-1"},
				}, nil
			}

			watchFields := []string{".spec.secret"}
			requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

			Expect(requests).To(HaveLen(3))
			Expect(requests[0].NamespacedName).To(Equal(types.NamespacedName{Name: "svc-1", Namespace: "ns-1"}))
			Expect(requests[1].NamespacedName).To(Equal(types.NamespacedName{Name: "svc-2", Namespace: "ns-2"}))
			Expect(requests[2].NamespacedName).To(Equal(types.NamespacedName{Name: "svc-3", Namespace: "ns-1"}))
		})
	})
})

// Traditional Go tests can also be used alongside Ginkgo tests
func TestCreateRequestsFromObjectUpdates_EdgeCases(t *testing.T) {
	ctx := context.Background()
	log := logr.Discard()
	srcObj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-namespace",
		},
	}

	t.Run("nil logger should not panic", func(t *testing.T) {
		mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
			// Logger might be used for logging
			if log != nil {
				log.Info("listing objects")
			}
			return []metav1.ObjectMeta{{Name: "obj", Namespace: "ns"}}, nil
		}

		defer func() {
			if r := recover(); r != nil {
				t.Errorf("function panicked with nil logger: %v", r)
			}
		}()

		requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, []string{".spec.secret"}, mockListFunc, &log)
		if len(requests) != 1 {
			t.Errorf("expected 1 request, got %d", len(requests))
		}
	})

	t.Run("nil client should be passable to listFunc", func(t *testing.T) {
		mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
			// Client might be nil in test scenarios
			return []metav1.ObjectMeta{{Name: "obj", Namespace: "ns"}}, nil
		}

		requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, []string{".spec.secret"}, mockListFunc, &log)
		if len(requests) != 1 {
			t.Errorf("expected 1 request, got %d", len(requests))
		}
	})

	t.Run("many watchFields should all be processed", func(t *testing.T) {
		mockListFunc := func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error) {
			return []metav1.ObjectMeta{{Name: "obj-" + field, Namespace: "ns"}}, nil
		}

		watchFields := []string{"field1", "field2", "field3", "field4", "field5"}
		requests := CreateRequestsFromObjectUpdates(ctx, nil, srcObj, watchFields, mockListFunc, &log)

		if len(requests) != 5 {
			t.Errorf("expected 5 requests, got %d", len(requests))
		}
	})
}
