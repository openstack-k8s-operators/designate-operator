/*
Copyright 2024.

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
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	networkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	// revive:disable-next-line:dot-imports
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	. "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

var _ = Describe("DesignateMdns controller", func() {
	var name string
	var spec map[string]any
	var designateMdnsName types.NamespacedName
	var transportURLSecretName types.NamespacedName

	BeforeEach(func() {
		name = fmt.Sprintf("designate-mdns-%s", uuid.New().String())
		spec = GetDefaultDesignateMdnsSpec()

		transportURLSecretName = types.NamespacedName{
			Namespace: namespace,
			Name:      RabbitmqSecretName,
		}

		designateMdnsName = types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}
		spec["transportURLSecret"] = transportURLSecretName.Name
	})

	When("config files are created", func() {
		var keystoneInternalEndpoint string
		var keystonePublicEndpoint string

		BeforeEach(func() {
			keystoneName := keystone.CreateKeystoneAPI(namespace)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystoneInternalEndpoint = fmt.Sprintf("http://keystone-for-%s-internal", designateMdnsName.Name)
			keystonePublicEndpoint = fmt.Sprintf("http://keystone-for-%s-public", designateMdnsName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneInternalEndpoint)

			DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))

			spec["customServiceConfig"] = "[DEFAULT]\ndebug=True\n"
			DeferCleanup(th.DeleteInstance, CreateDesignateMdns(designateMdnsName, spec))

			mariaDBDatabaseName := mariadb.CreateMariaDBDatabase(namespace, designate.DatabaseCRName, mariadbv1.MariaDBDatabaseSpec{})
			mariaDBDatabase := mariadb.GetMariaDBDatabase(mariaDBDatabaseName)
			DeferCleanup(k8sClient.Delete, ctx, mariaDBDatabase)

			designateMdns := GetDesignateMdns(designateMdnsName)
			apiMariaDBAccount, apiMariaDBSecret := mariadb.CreateMariaDBAccountAndSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      designateMdns.Spec.DatabaseAccount,
				}, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBAccount)
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBSecret)

			// Create network attachment definition with the name expected by the spec
			nadName := spec["designateNetworkAttachment"].(string)
			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      nadName,
				Namespace: namespace,
			}))

			// Create control network attachment definition (default name is "designate")
			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      "designate",
				Namespace: namespace,
			}))
		})

		It("should be in state of having the input ready", func() {
			th.ExpectCondition(
				designateMdnsName,
				ConditionGetterFunc(DesignateMdnsConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should add predictableip labels to pods", func() {
			// Create predictable IP configmap for mdns
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      designate.MdnsPredIPConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"mdns_address_0": "172.28.0.41",
					"mdns_address_1": "172.28.0.42",
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, configMap)

			// Create a pod with the expected labels
			podName := types.NamespacedName{
				Name:      fmt.Sprintf("%s-0", designateMdnsName.Name),
				Namespace: namespace,
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName.Name,
					Namespace: podName.Namespace,
					Labels: map[string]string{
						common.AppSelector: designateMdnsName.Name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "test",
						Image: "test",
					}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, pod)

			// Test the HandlePodLabeling function directly
			config := designate.PodLabelingConfig{
				ConfigMapName: designate.MdnsPredIPConfigMap,
				IPKeyPrefix:   "mdns_address_",
			}
			h, err := helper.NewHelper(
				&designatev1.DesignateMdns{},
				k8sClient,
				nil,
				nil,
				logger,
			)
			Expect(err).ShouldNot(HaveOccurred())

			// Debug: List pods before calling HandlePodLabeling
			podList := &corev1.PodList{}
			listOpts := []client.ListOption{
				client.InNamespace(namespace),
				client.MatchingLabels{
					common.AppSelector: designateMdnsName.Name,
				},
			}
			err = k8sClient.List(ctx, podList, listOpts...)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(podList.Items).To(HaveLen(1), "Should find exactly one pod")

			err = designate.HandlePodLabeling(ctx, h, designateMdnsName.Name, namespace, config)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the pod has the predictableip label
			Eventually(func(g Gomega) {
				updatedPod := &corev1.Pod{}
				g.Expect(k8sClient.Get(ctx, podName, updatedPod)).Should(Succeed())
				g.Expect(updatedPod.Labels).Should(HaveKeyWithValue(networkv1.PredictableIPLabel, "172.28.0.41"))
			}, timeout, interval).Should(Succeed())
		})

		It("should update IP label when pod has stale IP", func() {
			// Create predictable IP configmap for mdns
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      designate.MdnsPredIPConfigMap,
					Namespace: namespace,
				},
				Data: map[string]string{
					"mdns_address_0": "172.28.0.41",
					"mdns_address_1": "172.28.0.42",
				},
			}
			Expect(k8sClient.Create(ctx, configMap)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, configMap)

			// Create a pod with a stale IP label
			// This simulates a scenario where the IP changed in the configmap
			podName := types.NamespacedName{
				Name:      fmt.Sprintf("%s-0", designateMdnsName.Name),
				Namespace: namespace,
			}
			stalePod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName.Name,
					Namespace: podName.Namespace,
					Labels: map[string]string{
						common.AppSelector:           designateMdnsName.Name,
						networkv1.PredictableIPLabel: "172.28.0.99", // Stale IP
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  "test",
						Image: "test",
					}},
				},
			}
			Expect(k8sClient.Create(ctx, stalePod)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, stalePod)

			// Test the HandlePodLabeling function directly
			config := designate.PodLabelingConfig{
				ConfigMapName: designate.MdnsPredIPConfigMap,
				IPKeyPrefix:   "mdns_address_",
			}
			h, err := helper.NewHelper(
				&designatev1.DesignateMdns{},
				k8sClient,
				nil,
				nil,
				logger,
			)
			Expect(err).ShouldNot(HaveOccurred())

			err = designate.HandlePodLabeling(ctx, h, designateMdnsName.Name, namespace, config)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the label was updated to match the configmap's IP
			Eventually(func(g Gomega) {
				updatedPod := &corev1.Pod{}
				g.Expect(k8sClient.Get(ctx, podName, updatedPod)).Should(Succeed())
				g.Expect(updatedPod.Labels).Should(HaveKeyWithValue(networkv1.PredictableIPLabel, "172.28.0.41"))
			}, timeout, interval).Should(Succeed())
		})
	})
})
