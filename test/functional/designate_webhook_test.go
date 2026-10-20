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

package functional_test

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports

	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func designateWithServicePortNames(name string, portNames map[string]string) *unstructured.Unstructured {
	serviceOverrides := map[string]any{}
	for endpoint, portName := range portNames {
		serviceOverrides[endpoint] = map[string]any{
			"spec": map[string]any{
				"ports": []any{map[string]any{
					"name": portName,
					"port": int64(9001),
				}},
			},
		}
	}
	spec := GetDefaultDesignateSpec(0, 0, 0)
	spec["designateAPI"] = map[string]any{
		"override": map[string]any{
			"service": serviceOverrides,
		},
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "Designate",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}}
}

func designateWithEndpointURL(name, endpoint, endpointURL string, port int64) *unstructured.Unstructured {
	override := map[string]any{"endpointURL": endpointURL}
	if port != 0 {
		override["spec"] = map[string]any{
			"ports": []any{map[string]any{"name": endpoint, "port": port}},
		}
	}
	spec := GetDefaultDesignateSpec(0, 0, 0)
	spec["designateAPI"] = map[string]any{
		"override": map[string]any{
			"service": map[string]any{endpoint: override},
		},
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "Designate",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": spec,
	}}
}

func setDesignateServicePortName(obj *unstructured.Unstructured, endpoint, portName string) {
	spec := obj.Object["spec"].(map[string]any)
	designateAPI := spec["designateAPI"].(map[string]any)
	override := designateAPI["override"].(map[string]any)
	serviceOverrides := override["service"].(map[string]any)
	serviceOverride := serviceOverrides[endpoint].(map[string]any)
	serviceSpec := serviceOverride["spec"].(map[string]any)
	ports := serviceSpec["ports"].([]any)
	ports[0].(map[string]any)["name"] = portName
}

var _ = Describe("Designate webhook", func() {
	It("validates public and internal Service port names on create and update", func() {
		valid := designateWithServicePortNames("designate-port-name-valid", map[string]string{
			"public":   "public",
			"internal": "internal",
		})
		_, err := controllerutil.CreateOrPatch(ctx, k8sClient, valid, func() error { return nil })
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, valid)
		})

		for _, endpoint := range []string{"public", "internal"} {
			portNames := map[string]string{"public": "public", "internal": "internal"}
			portNames[endpoint] = "wrong"
			invalid := designateWithServicePortNames("designate-port-name-create-"+endpoint, portNames)
			_, err := controllerutil.CreateOrPatch(ctx, k8sClient, invalid, func() error { return nil })
			if err == nil {
				DeferCleanup(func() {
					_ = k8sClient.Delete(ctx, invalid)
				})
			}
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be \"" + endpoint + "\""))

			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				current := &unstructured.Unstructured{}
				current.SetGroupVersionKind(valid.GroupVersionKind())
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Namespace: namespace, Name: valid.GetName(),
				}, current); err != nil {
					return err
				}
				setDesignateServicePortName(current, endpoint, "wrong")
				return k8sClient.Update(ctx, current)
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("must be \"" + endpoint + "\""))
		}
	})

	It("validates endpointURL host and port for public and internal endpoints", func() {
		cases := []struct {
			url    string
			port   int64
			errMsg string
		}{
			{"https://api.example.test:9001", 0, ""},
			{"https://api.example.test:9001", 9001, ""},
			{"https://designate", 0, "endpointURL"},
			{"api.example.test:9001", 0, "endpointURL"},
			{"https://192.0.2.10:9001", 0, "endpointURL"},
			{"https://api.example.test:9001", 9002, "must match the port in endpointURL"},
		}
		for _, endpoint := range []string{"public", "internal"} {
			for i, c := range cases {
				obj := designateWithEndpointURL(
					fmt.Sprintf("designate-url-%s-%d", endpoint, i), endpoint, c.url, c.port)
				_, err := controllerutil.CreateOrPatch(ctx, k8sClient, obj, func() error { return nil })
				if c.errMsg == "" {
					Expect(err).NotTo(HaveOccurred(), "%s %s", endpoint, c.url)
					DeferCleanup(func() { _ = k8sClient.Delete(ctx, obj) })
				} else {
					Expect(err).To(HaveOccurred(), "%s %s", endpoint, c.url)
					Expect(err.Error()).To(ContainSubstring(c.errMsg))
				}
			}
		}
	})

	It("rejects update to deprecated rabbitMqClusterName field", func() {
		spec := GetDefaultDesignateSpec(0, 0, 0)
		spec["rabbitMqClusterName"] = "rabbitmq"

		designateName := types.NamespacedName{
			Namespace: namespace,
			Name:      "designate-webhook-test",
		}

		raw := map[string]any{
			"apiVersion": "designate.openstack.org/v1beta1",
			"kind":       "Designate",
			"metadata": map[string]any{
				"name":      designateName.Name,
				"namespace": designateName.Namespace,
			},
			"spec": spec,
		}

		// Create the Designate instance
		unstructuredObj := &unstructured.Unstructured{Object: raw}
		_, err := controllerutil.CreateOrPatch(
			ctx, k8sClient, unstructuredObj, func() error { return nil })
		Expect(err).ShouldNot(HaveOccurred())

		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, unstructuredObj)
		})

		// Try to update rabbitMqClusterName
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, designateName, unstructuredObj)).Should(Succeed())
			specMap := unstructuredObj.Object["spec"].(map[string]any)
			specMap["rabbitMqClusterName"] = "rabbitmq2"
			err := k8sClient.Update(ctx, unstructuredObj)
			g.Expect(err).Should(HaveOccurred())

			var statusError *k8s_errors.StatusError
			g.Expect(errors.As(err, &statusError)).To(BeTrue())
			g.Expect(statusError.ErrStatus.Details.Kind).To(Equal("Designate"))
			g.Expect(statusError.ErrStatus.Message).To(
				ContainSubstring("field \"spec.rabbitMqClusterName\" is deprecated"))
			g.Expect(statusError.ErrStatus.Message).To(
				ContainSubstring("use \"spec.messagingBus.cluster\" instead"))
		}, timeout, interval).Should(Succeed())
	})
})
