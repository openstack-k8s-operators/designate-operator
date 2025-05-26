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
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	//revive:disable-next-line:dot-imports
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	. "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

var _ = Describe("DesignateAPI controller", func() {
	var name string
	var spec map[string]interface{}
	var designateAPIName types.NamespacedName
	var transportURLSecretName types.NamespacedName

	BeforeEach(func() {
		name = fmt.Sprintf("designate-api-%s", uuid.New().String())
		spec = GetDefaultDesignateAPISpec()

		designateAPIName = types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}

		transportURLSecretName = types.NamespacedName{
			Namespace: namespace,
			Name:      RabbitmqSecretName,
		}
		spec["transportURLSecret"] = transportURLSecretName.Name
	})

	When("a DesignateAPI instance is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignateAPI(designateAPIName, spec))
		})

		It("should have the Status fields initialized", func() {
			designateAPI := GetDesignateAPI((designateAPIName))
			Expect(designateAPI.Status.ReadyCount).Should(Equal(int32(0)))
		})

		It("should have Waiting Conditions initialized as TransportUrl not created", func() {
			for _, cond := range []condition.Type{
				condition.InputReadyCondition,
			} {
				th.ExpectCondition(
					designateAPIName,
					ConditionGetterFunc(DesignateAPIConditionGetter),
					cond,
					corev1.ConditionFalse,
				)
			}
		})

		It("should have a expected default values", func() {
			designateAPI := GetDesignateAPI((designateAPIName))
			Expect(designateAPI.Spec.ServiceUser).Should(Equal("designate"))
			Expect(designateAPI.Spec.DatabaseAccount).Should(Equal("designate"))
			Expect(designateAPI.Spec.PasswordSelectors.Service).Should(Equal("DesignatePassword"))
		})

		It("should have a finalizer", func() {
			// the reconciler loop adds the finalizer so we have to wait for
			// it to run
			Eventually(func() []string {
				return GetDesignateAPI(designateAPIName).Finalizers
			}, timeout, interval).Should(ContainElement("openstack.org/designateapi"))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateAPIName.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateAPIName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// Secret and Transport available
	When("a proper secret is provided and TransportURL is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignateAPI(designateAPIName, spec))
			DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateAPIName.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateAPIName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// TLS Validation

	// Config
	When("config files are created", func() {
		var keystoneInternalEndpoint string
		var keystonePublicEndpoint string

		BeforeEach(func() {
			keystoneName := keystone.CreateKeystoneAPI(namespace)
			DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
			keystoneInternalEndpoint = fmt.Sprintf("http://keystone-for-%s-internal", designateAPIName.Name)
			keystonePublicEndpoint = fmt.Sprintf("http://keystone-for-%s-public", designateAPIName.Name)
			SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, keystoneInternalEndpoint)

			DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(namespace))
			DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))

			spec["customServiceConfig"] = "[DEFAULT]\ndebug=True\n"
			DeferCleanup(th.DeleteInstance, CreateDesignateAPI(designateAPIName, spec))

			mariaDBDatabaseName := mariadb.CreateMariaDBDatabase(namespace, designate.DatabaseCRName, mariadbv1.MariaDBDatabaseSpec{})
			mariaDBDatabase := mariadb.GetMariaDBDatabase(mariaDBDatabaseName)
			DeferCleanup(k8sClient.Delete, ctx, mariaDBDatabase)

			designateAPI := GetDesignateAPI(designateAPIName)
			apiMariaDBAccount, apiMariaDBSecret := mariadb.CreateMariaDBAccountAndSecret(
				types.NamespacedName{
					Namespace: namespace,
					Name:      designateAPI.Spec.DatabaseAccount,
				}, mariadbv1.MariaDBAccountSpec{})
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBAccount)
			DeferCleanup(k8sClient.Delete, ctx, apiMariaDBSecret)
		})

		It("should be in state of having the input ready", func() {
			th.ExpectCondition(
				designateAPIName,
				ConditionGetterFunc(DesignateAPIConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should set Service Config Ready Condition", func() {
			th.ExpectCondition(
				designateAPIName,
				ConditionGetterFunc(DesignateAPIConditionGetter),
				condition.ServiceConfigReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should create the designate.conf file in a Secret", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: designateAPIName.Namespace,
					Name:      fmt.Sprintf("%s-config-data", designateAPIName.Name)})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["designate.conf"])
			instance := GetDesignateAPI(designateAPIName)

			dbs := []struct {
				Name            string
				DatabaseAccount string
				Keyword         string
			}{
				{
					Name:            designate.DatabaseName,
					DatabaseAccount: instance.Spec.DatabaseAccount,
					Keyword:         "connection",
				},
			}

			for _, db := range dbs {
				databaseAccount := mariadb.GetMariaDBAccount(
					types.NamespacedName{
						Namespace: namespace,
						Name:      db.DatabaseAccount})
				databaseSecret := th.GetSecret(
					types.NamespacedName{
						Namespace: namespace,
						Name:      databaseAccount.Spec.Secret})

				Expect(conf).Should(
					ContainSubstring(
						fmt.Sprintf(
							"%s=mysql+pymysql://%s:%s@%s/%s?read_default_file=/etc/my.cnf",
							db.Keyword,
							databaseAccount.Spec.UserName,
							databaseSecret.Data[mariadbv1.DatabasePasswordSelector],
							instance.Spec.DatabaseHostname,
							db.Name)))
			}

			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"www_authenticate_uri=%s\n", keystonePublicEndpoint)))
			// TBC: [keystone_authtoken].auth_url and [service_auth].auth_url differ?
			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"auth_url=%s\n", keystoneInternalEndpoint)))
			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"auth_url=%s\n", keystoneInternalEndpoint)))
			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"username=%s\n", instance.Spec.ServiceUser)))

			ospSecret := th.GetSecret(types.NamespacedName{
				Name:      SecretName,
				Namespace: namespace})
			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"\npassword=%s\n", string(ospSecret.Data["DesignatePassword"]))))

			transportURLSecret := th.GetSecret(transportURLSecretName)
			Expect(conf).Should(
				ContainSubstring(fmt.Sprintf(
					"transport_url=%s\n", string(transportURLSecret.Data["transport_url"]))))
		})

		It("should create a Secret with customServiceConfig input", func() {
			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: designateAPIName.Namespace,
					Name:      fmt.Sprintf("%s-config-data", designateAPIName.Name)})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["custom.conf"])
			Expect(conf).Should(
				ContainSubstring("[DEFAULT]\ndebug=True\n"))
		})
	})

	// NAD

	// Networks Annotation

	// Service

	// Keystone Service

	// Deployment
})
