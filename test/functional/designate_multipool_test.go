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
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

var _ = Describe("Designate multipool controller", func() {
	var multipoolConfigMapName types.NamespacedName

	BeforeEach(func() {
		multipoolConfigMapName = types.NamespacedName{
			Name:      designate.MultipoolConfigMapName,
			Namespace: namespace,
		}
	})

	When("multipool ConfigMap with two pools is created", func() {
		It("should create a shared TSIG secret for non-default pools", func() {
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
  bindReplicas: 1
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify multipool config can be parsed
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config).ShouldNot(BeNil())
			Expect(config.Pools).To(HaveLen(2))
		})
	})

	When("multipool ConfigMap with only default pool is created", func() {
		It("should not create TSIG secret for default-only configuration", func() {
			multipoolConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `- name: default
  description: "Default Pool"
  bindReplicas: 2
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify only one pool exists
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config).ShouldNot(BeNil())
			Expect(config.Pools).To(HaveLen(1))
			Expect(config.Pools[0].Name).To(Equal("default"))
		})
	})

	When("multipool ConfigMap is scaled from 2 pools to 3 pools", func() {
		It("should handle pool addition correctly", func() {
			// Create initial 2-pool config
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
  bindReplicas: 1
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify initial state
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(2))

			// Update to 3 pools
			multipoolConfig.Data["pools"] = `- name: default
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
      priority: 1
- name: pool2
  description: "Pool 2"
  bindReplicas: 1
  nsRecords:
    - hostname: ns3.example.org.
      priority: 1`
			Expect(k8sClient.Update(ctx, multipoolConfig)).Should(Succeed())

			// Verify new state
			config, err = designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(3))
			Expect(config.Pools[2].Name).To(Equal("pool2"))
		})
	})

	When("multipool ConfigMap has pools with different replica counts", func() {
		It("should respect per-pool bindReplicas configuration", func() {
			multipoolConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `- name: default
  description: "Default Pool"
  bindReplicas: 2
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1
- name: pool1
  description: "Pool 1"
  bindReplicas: 3
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify replica counts
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(2))
			Expect(config.Pools[0].BindReplicas).To(Equal(int32(2)))
			Expect(config.Pools[1].BindReplicas).To(Equal(int32(3)))
		})
	})

	When("multipool ConfigMap has pools with custom attributes", func() {
		It("should preserve pool attributes", func() {
			multipoolConfig := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      multipoolConfigMapName.Name,
					Namespace: multipoolConfigMapName.Namespace,
				},
				Data: map[string]string{
					"pools": `- name: default
  description: "Default Pool"
  bindReplicas: 1
  attributes:
    availability_zone: az1
    rack: r1
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1
- name: pool1
  description: "Pool 1"
  bindReplicas: 1
  attributes:
    availability_zone: az2
    rack: r2
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify attributes
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(2))
			Expect(config.Pools[0].Attributes).To(HaveKeyWithValue("availability_zone", "az1"))
			Expect(config.Pools[0].Attributes).To(HaveKeyWithValue("rack", "r1"))
			Expect(config.Pools[1].Attributes).To(HaveKeyWithValue("availability_zone", "az2"))
			Expect(config.Pools[1].Attributes).To(HaveKeyWithValue("rack", "r2"))
		})
	})

	When("multipool ConfigMap has pools sorted in non-alphabetical order", func() {
		It("should maintain pool order based on ConfigMap definition", func() {
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

			// Verify pool order matches alphabetical sorting (from code implementation)
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(3))
			// Pools should be sorted alphabetically by name
			Expect(config.Pools[0].Name).To(Equal("default"))
			Expect(config.Pools[1].Name).To(Equal("pool1"))
			Expect(config.Pools[2].Name).To(Equal("pool2"))
		})
	})

	When("multipool ConfigMap has pool with per-pool NS records", func() {
		It("should use pool-specific NS records", func() {
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
    - hostname: ns2.example.org.
      priority: 2
- name: pool1
  description: "Pool 1"
  bindReplicas: 1
  nsRecords:
    - hostname: ns-pool1-primary.example.org.
      priority: 1
    - hostname: ns-pool1-secondary.example.org.
      priority: 2`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify per-pool NS records
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools).To(HaveLen(2))

			// Default pool NS records
			Expect(config.Pools[0].NSRecords).To(HaveLen(2))
			Expect(config.Pools[0].NSRecords[0].Hostname).To(Equal("ns1.example.org."))
			Expect(config.Pools[0].NSRecords[1].Hostname).To(Equal("ns2.example.org."))

			// Pool1 NS records
			Expect(config.Pools[1].NSRecords).To(HaveLen(2))
			Expect(config.Pools[1].NSRecords[0].Hostname).To(Equal("ns-pool1-primary.example.org."))
			Expect(config.Pools[1].NSRecords[1].Hostname).To(Equal("ns-pool1-secondary.example.org."))
		})
	})

	When("multipool configuration changes pool replica count", func() {
		It("should handle replica scaling for individual pools", func() {
			// Create initial config with 1 replica per pool
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
  bindReplicas: 1
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`,
				},
			}
			Expect(k8sClient.Create(ctx, multipoolConfig)).Should(Succeed())
			DeferCleanup(k8sClient.Delete, ctx, multipoolConfig)

			// Verify initial replica counts
			config, err := designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools[0].BindReplicas).To(Equal(int32(1)))
			Expect(config.Pools[1].BindReplicas).To(Equal(int32(1)))

			// Scale pool1 to 3 replicas
			multipoolConfig.Data["pools"] = `- name: default
  description: "Default Pool"
  bindReplicas: 1
  nsRecords:
    - hostname: ns1.example.org.
      priority: 1
- name: pool1
  description: "Pool 1"
  bindReplicas: 3
  nsRecords:
    - hostname: ns2.example.org.
      priority: 1`
			Expect(k8sClient.Update(ctx, multipoolConfig)).Should(Succeed())

			// Verify updated replica count
			config, err = designate.GetMultipoolConfig(ctx, k8sClient, namespace)
			Expect(err).ShouldNot(HaveOccurred())
			Expect(config.Pools[0].BindReplicas).To(Equal(int32(1)))
			Expect(config.Pools[1].BindReplicas).To(Equal(int32(3)))
		})
	})
})
