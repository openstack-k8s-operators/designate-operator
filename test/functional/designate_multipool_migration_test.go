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

package functional_test

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
)

var _ = Describe("Designate multipool ConfigMap migration", func() {
	var designateName types.NamespacedName
	var multipoolConfigMapName types.NamespacedName

	BeforeEach(func() {
		designateName = types.NamespacedName{
			Namespace: namespace,
			Name:      fmt.Sprintf("designate-migration-%s", uuid.New().String()[:10]),
		}
		multipoolConfigMapName = types.NamespacedName{
			Name:      designate.MultipoolConfigMapName,
			Namespace: namespace,
		}
	})

	It("creates the multipool ConfigMap from the CRD-based configuration", func() {
		spec := GetDefaultDesignateSpec(2, 1, 0)
		spec["nsRecords"] = []designatev1.DesignateNSRecord{
			{Hostname: "ns1.example.org.", Priority: 1},
			{Hostname: "ns2.example.org.", Priority: 2},
		}
		DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: multipoolConfigMapName.Name, Namespace: namespace,
			}})
		})

		Eventually(func(g Gomega) {
			configMap := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, multipoolConfigMapName, configMap)).To(Succeed())

			var pools []designate.PoolConfig
			g.Expect(yaml.Unmarshal([]byte(configMap.Data[designate.MultipoolConfigMapKey]), &pools)).To(Succeed())
			g.Expect(pools).To(HaveLen(1))
			g.Expect(pools[0].Name).To(Equal(designate.DefaultPoolName))
			g.Expect(pools[0].Description).To(Equal(designate.DefaultPoolDescription))
			g.Expect(pools[0].BindReplicas).To(Equal(int32(2)))
			g.Expect(pools[0].NSRecords).To(ConsistOf(
				designatev1.DesignateNSRecord{Hostname: "ns1.example.org.", Priority: 1},
				designatev1.DesignateNSRecord{Hostname: "ns2.example.org.", Priority: 2},
			))
		}, timeout, interval).Should(Succeed())
	})

	It("does not overwrite an existing multipool ConfigMap", func() {
		existingPools := `- name: default
  bindReplicas: 5
  nsRecords:
    - hostname: existing.example.org.
      priority: 1
`
		configMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: multipoolConfigMapName.Name, Namespace: namespace},
			Data:       map[string]string{designate.MultipoolConfigMapKey: existingPools},
		}
		Expect(k8sClient.Create(ctx, configMap)).To(Succeed())
		DeferCleanup(k8sClient.Delete, ctx, configMap)

		spec := GetDefaultDesignateSpec(2, 1, 0)
		spec["nsRecords"] = []designatev1.DesignateNSRecord{{Hostname: "ns1.example.org.", Priority: 1}}
		DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))

		Eventually(func(g Gomega) {
			instance := GetDesignate(designateName)
			g.Expect(instance.Status.ObservedGeneration).To(Equal(instance.Generation))
		}, timeout, interval).Should(Succeed())
		Consistently(func(g Gomega) {
			current := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, multipoolConfigMapName, current)).To(Succeed())
			g.Expect(current.Data[designate.MultipoolConfigMapKey]).To(Equal(existingPools))
		}, time.Second, interval).Should(Succeed())
	})

	It("reports translation errors in the CR status", func() {
		spec := GetDefaultDesignateSpec(0, 1, 0)
		spec["nsRecords"] = []designatev1.DesignateNSRecord{{Hostname: "ns1.example.org.", Priority: 1}}
		DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))

		Eventually(func(g Gomega) {
			conditions := DesignateConditionGetter(designateName)
			g.Expect(conditions.IsFalse(condition.InputReadyCondition)).To(BeTrue())
			inputCondition := conditions.Get(condition.InputReadyCondition)
			g.Expect(inputCondition).NotTo(BeNil())
			g.Expect(inputCondition.Message).To(ContainSubstring(designate.ErrInvalidBindReplicas.Error()))
		}, timeout, interval).Should(Succeed())
	})

	It("translates the CR once the NS records ConfigMap appears", func() {
		spec := GetDefaultDesignateSpec(1, 1, 0)
		DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
				Name: multipoolConfigMapName.Name, Namespace: namespace,
			}})
		})

		// Without NS records there is no valid pool to translate.
		Consistently(func(g Gomega) {
			err := k8sClient.Get(ctx, multipoolConfigMapName, &corev1.ConfigMap{})
			g.Expect(k8s_errors.IsNotFound(err)).To(BeTrue())
		}, time.Second, interval).Should(Succeed())

		// Creating the NS records ConfigMap must trigger reconciliation instead of
		// waiting for the periodic requeue.
		createAndSimulateNSRecordsConfigMap(types.NamespacedName{
			Namespace: namespace,
			Name:      designate.NsRecordsConfigMap,
		})

		Eventually(func(g Gomega) {
			configMap := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, multipoolConfigMapName, configMap)).To(Succeed())
			g.Expect(configMap.Data).To(HaveKey(designate.MultipoolConfigMapKey))
		}, 5*time.Second, interval).Should(Succeed())
	})
})
