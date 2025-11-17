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
	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	// revive:disable-next-line:dot-imports
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
		})

		It("should be in state of having the input ready", func() {
			th.ExpectCondition(
				designateBackendbind9Name,
				ConditionGetterFunc(DesignateBackendbind9ConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})
	})
})
