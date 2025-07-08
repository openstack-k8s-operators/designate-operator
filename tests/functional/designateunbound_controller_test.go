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
	"fmt"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"k8s.io/apimachinery/pkg/types"
	// "sigs.k8s.io/controller-runtime/pkg/client"
	// revive:disable-next-line:dot-imports
)

var _ = Describe("DesignateUnbound controller", func() {

	var name string
	var spec map[string]interface{}
	var designateUnboundName types.NamespacedName

	BeforeEach(func() {
		idValue := uuid.New().String()
		name = fmt.Sprintf("designate-unbound-%s", idValue[len(idValue)-8:])
		spec = GetDefaultUnboundSpec()

		designateUnboundName = types.NamespacedName{
			Name:      name,
			Namespace: namespace,
		}
	})

	When("a DesignateUnbound instance is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignateUnbound(designateUnboundName, spec))
		})

		It("should have the status fields initialized", func() {
			unbound := GetDesignateUnbound(designateUnboundName)
			Expect(unbound.Status.ReadyCount).Should(Equal(int32(0)))
		})

		It("should have a finalizer", func() {
			Eventually(func() []string {
				return GetDesignateUnbound(designateUnboundName).Finalizers
			}, timeout, interval).Should(ContainElement("openstack.org/designateunbound"))
		})

		It("should have a config data secret", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      fmt.Sprintf("%s-config-data", designateUnboundName.Name),
				})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["unbound.conf"])
			Expect(conf).Should(ContainSubstring("do-daemonize: no"))
		})

		It("should not have stubzones", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      fmt.Sprintf("%s-config-data", designateUnboundName.Name),
				})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["01-stubzones.conf"])
			Expect(conf).ShouldNot(ContainSubstring("stub-zone:"))
		})
	})

	When("a DesignateUnbound with stub zones is created", func() {
		BeforeEach(func() {

			bindIPs := map[string]interface{}{
				"bind_address_0": "172.32.0.90",
				"bind_address_1": "172.32.0.92",
			}
			DeferCleanup(k8sClient.Delete, ctx, CreateBindIPMap(designateUnboundName.Namespace, bindIPs))
			spec["stubZones"] = []map[string]interface{}{
				{
					"name":    "sales.foobar.com",
					"options": map[string]string{},
				},
				{
					"name":    "service.decom.com",
					"options": map[string]string{},
				},
			}
			DeferCleanup(th.DeleteInstance, CreateDesignateUnbound(designateUnboundName, spec))
		})

		It("should have stub zones configured", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      fmt.Sprintf("%s-config-data", designateUnboundName.Name),
				})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["01-stubzones.conf"])
			Expect(conf).Should(ContainSubstring("stub-zone:"))
			Expect(conf).Should(ContainSubstring("name: sales.foobar.com"))
			Expect(conf).Should(ContainSubstring("name: service.decom.com"))
			Expect(conf).Should(ContainSubstring("addr: 172.32.0.90"))
			Expect(conf).Should(ContainSubstring("addr: 172.32.0.92"))
		})
	})
})
