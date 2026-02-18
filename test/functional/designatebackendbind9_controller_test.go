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

	// revive:disable-next-line:dot-imports
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	. "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

var _ = Describe("DesignateBackendbind9 controller", func() {
	var name string
	var spec map[string]any
	var designateBackendbind9Name types.NamespacedName
	var transportURLSecretName types.NamespacedName

	BeforeEach(func() {
		name = fmt.Sprintf("designate-backendbind9-%s", uuid.New().String())
		spec = GetDefaultDesignateBackendbind9Spec()

		transportURLSecretName = types.NamespacedName{
			Namespace: namespace,
			Name:      RabbitmqSecretName,
		}

		designateBackendbind9Name = types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}
		spec["transportURLSecret"] = transportURLSecretName.Name
	})
	When("a DesignateBackendbind9 instance is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignateBackendbind9(designateBackendbind9Name, spec))
		})

		It("should have the Status fields initialized", func() {
			designateBackendbind9 := GetDesignateBackendbind9(designateBackendbind9Name)
			Expect(designateBackendbind9.Status.ReadyCount).Should(Equal(int32(0)))
		})

		It("should have a finalizer", func() {
			// the reconciler loop adds the finalizer so we have to wait for
			// it to run
			Eventually(func() []string {
				return GetDesignateBackendbind9(designateBackendbind9Name).Finalizers
			}, timeout, interval).Should(ContainElement("openstack.org/designatebackendbind9"))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateBackendbind9Name.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateBackendbind9Name.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// Maybe we could delete the one above, it is copied from the api tests
	When("a proper secret is provided and TransportURL is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignateBackendbind9(designateBackendbind9Name, spec))
			DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateBackendbind9Name.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateBackendbind9Name.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	When("config files are created", func() {
		var keystoneInternalEndpoint string
		var keystonePublicEndpoint string

		BeforeEach(func() {
			keystoneName := keystone.CreateKeystoneAPI(namespace)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystoneInternalEndpoint = fmt.Sprintf("http://keystone-for-%s-internal", designateBackendbind9Name.Name)
			keystonePublicEndpoint = fmt.Sprintf("http://keystone-for-%s-public", designateBackendbind9Name.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneInternalEndpoint)

			DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))

			spec["customServiceConfig"] = "[DEFAULT]\ndebug=True\n"
			DeferCleanup(th.DeleteInstance, CreateDesignateBackendbind9(designateBackendbind9Name, spec))

			mariaDBDatabaseName := mariadb.CreateMariaDBDatabase(namespace, designate.DatabaseCRName, mariadbv1.MariaDBDatabaseSpec{})
			mariaDBDatabase := mariadb.GetMariaDBDatabase(mariaDBDatabaseName)
			DeferCleanup(k8sClient.Delete, ctx, mariaDBDatabase)

			designateBackendbind9 := GetDesignateBackendbind9(designateBackendbind9Name)
			apiMariaDBAccount, apiMariaDBSecret := mariadb.CreateMariaDBAccountAndSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      designateBackendbind9.Spec.DatabaseAccount,
				}, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBAccount)
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBSecret)

			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
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
				designateBackendbind9Name,
				ConditionGetterFunc(DesignateBackendbind9ConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should add predictableip labels to pods", func() {
			// Create predictable IP configmap
			configData := map[string]any{
				"bind_address_0": "172.28.0.31",
				"bind_address_1": "172.28.0.32",
			}
			DeferCleanup(k8sClient.Delete, ctx, CreateBindIPMap(namespace, configData))

			// Create a pod with the expected labels
			podName := types.NamespacedName{
				Name:      fmt.Sprintf("%s-0", designateBackendbind9Name.Name),
				Namespace: namespace,
			}
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName.Name,
					Namespace: podName.Namespace,
					Labels: map[string]string{
						common.AppSelector: designateBackendbind9Name.Name,
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
				ConfigMapName: designate.BindPredIPConfigMap,
				IPKeyPrefix:   "bind_address_",
			}
			h, err := helper.NewHelper(
				&designatev1.DesignateBackendbind9{},
				k8sClient,
				nil,
				nil,
				logger,
			)
			Expect(err).ShouldNot(HaveOccurred())
			err = designate.HandlePodLabeling(ctx, h, designateBackendbind9Name.Name, namespace, config)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the pod has the predictableip label
			Eventually(func(g Gomega) {
				updatedPod := &corev1.Pod{}
				g.Expect(k8sClient.Get(ctx, podName, updatedPod)).Should(Succeed())
				g.Expect(updatedPod.Labels).Should(HaveKeyWithValue(networkv1.PredictableIPLabel, "172.28.0.31"))
			}, timeout, interval).Should(Succeed())
		})

		It("should update IP label when pod has stale IP", func() {
			// Create predictable IP configmap
			configData := map[string]any{
				"bind_address_0": "172.28.0.31",
				"bind_address_1": "172.28.0.32",
			}
			DeferCleanup(k8sClient.Delete, ctx, CreateBindIPMap(namespace, configData))

			// Create a pod with a stale IP label
			// This simulates a scenario where the IP changed in the configmap
			podName := types.NamespacedName{
				Name:      fmt.Sprintf("%s-0", designateBackendbind9Name.Name),
				Namespace: namespace,
			}
			stalePod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName.Name,
					Namespace: podName.Namespace,
					Labels: map[string]string{
						common.AppSelector:           designateBackendbind9Name.Name,
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
				ConfigMapName: designate.BindPredIPConfigMap,
				IPKeyPrefix:   "bind_address_",
			}
			h, err := helper.NewHelper(
				&designatev1.DesignateBackendbind9{},
				k8sClient,
				nil,
				nil,
				logger,
			)
			Expect(err).ShouldNot(HaveOccurred())
			err = designate.HandlePodLabeling(ctx, h, designateBackendbind9Name.Name, namespace, config)
			Expect(err).ShouldNot(HaveOccurred())

			// Verify the label was updated to match the configmap's IP
			Eventually(func(g Gomega) {
				updatedPod := &corev1.Pod{}
				g.Expect(k8sClient.Get(ctx, podName, updatedPod)).Should(Succeed())
				g.Expect(updatedPod.Labels).Should(HaveKeyWithValue(networkv1.PredictableIPLabel, "172.28.0.31"))
			}, timeout, interval).Should(Succeed())
		})
	})

	// Multipool ConfigMap tests - these test the multipool configuration parsing
	When("multipool ConfigMap is created", func() {
		var multipoolConfigMapName types.NamespacedName

		BeforeEach(func() {
			multipoolConfigMapName = types.NamespacedName{
				Name:      designate.MultipoolConfigMapName,
				Namespace: namespace,
			}
		})

		It("should parse valid multipool configuration", func() {
			multipoolConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `- name: default
  description: "Default Pool"
  bindReplicas: 1
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1
- name: pool1
  description: "Pool 1"
  bindReplicas: 2
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify we can fetch and parse the config
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config).ShouldNot(BeNil())
			Expect(config.Pools).To(HaveLen(2))
			Expect(config.Pools[0].Name).To(Equal("default"))
			Expect(config.Pools[0].BindReplicas).To(Equal(int32(1)))
			Expect(config.Pools[1].Name).To(Equal("pool1"))
			Expect(config.Pools[1].BindReplicas).To(Equal(int32(2)))
		})

		It("should not fail if multipool ConfigMap is missing", func() {
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config).Should(BeNil())
		})

		It("should reject invalid YAML in multipool config", func() {
			invalidConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `invalid: yaml: content:
  - this is not valid`,
				},
			}
			Expect(k8sClient.Create(ctx, invalidConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, invalidConfig)

			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).Should(HaveOccurred())
			Expect(config).Should(BeNil())
		})

		It("should maintain stable pool ordering regardless of ConfigMap order", func() {
			// Create a ConfigMap with pools in non-alphabetical order
			multipoolConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `- name: pool2
  description: "Pool 2"
  bindReplicas: 1
  nsRecords:
    - hostname: ns3.example.org.
      priority: 1
- name: default
  description: "Default Pool"
  bindReplicas: 1
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1
- name: pool1
  description: "Pool 1"
  bindReplicas: 1
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Fetch and verify pools are sorted alphabetically
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config).ShouldNot(BeNil())
			Expect(config.Pools).To(HaveLen(3))
			Expect(config.Pools[0].Name).To(Equal("default"), "First pool should be 'default'")
			Expect(config.Pools[1].Name).To(Equal("pool1"), "Second pool should be 'pool1'")
			Expect(config.Pools[2].Name).To(Equal("pool2"), "Third pool should be 'pool2'")
		})
	})
})
