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
	"gopkg.in/yaml.v2"
	"math/rand"
	"net"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"k8s.io/apimachinery/pkg/types"

	corev1 "k8s.io/api/core/v1"

	"github.com/openstack-k8s-operators/lib-common/modules/common/condition"

	//revive:disable-next-line:dot-imports
	validator "github.com/go-playground/validator/v10"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	. "github.com/openstack-k8s-operators/lib-common/modules/common/test/helpers"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

func createAndSimulateKeystone(
	designateName types.NamespacedName,
) APIFixtures {
	apiFixtures := SetupAPIFixtures(logger)
	keystoneName := keystone.CreateKeystoneAPIWithFixture(namespace, apiFixtures.Keystone)
	DeferCleanup(keystone.DeleteKeystoneAPI, keystoneName)
	keystonePublicEndpoint := fmt.Sprintf("http://keystone-for-%s-public", designateName.Name)
	SimulateKeystoneReady(keystoneName, keystonePublicEndpoint, apiFixtures.Keystone.Endpoint())
	return apiFixtures
}

func createAndSimulateDesignateSecrets(
	designateName types.NamespacedName,
) {
	DeferCleanup(k8sClient.Delete, ctx, CreateDesignateSecret(designateName.Namespace))
}

func createAndSimulateTransportURL(
	transportURLName types.NamespacedName,
	transportURLSecretName types.NamespacedName,
) {
	DeferCleanup(k8sClient.Delete, ctx, CreateTransportURL(transportURLName))
	DeferCleanup(k8sClient.Delete, ctx, CreateTransportURLSecret(transportURLSecretName))
	infra.SimulateTransportURLReady(transportURLName)
}

func createAndSimulateNSRecordsConfigMap(
	NsRecordsConfigMapName types.NamespacedName,
) {
	configMap := CreateDesignateNSRecordsConfigMap(NsRecordsConfigMapName)
	err := k8sClient.Create(ctx, configMap)
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() error {
		return k8sClient.Get(ctx, NsRecordsConfigMapName, &corev1.ConfigMap{})
	}, timeout, interval).Should(Succeed())

	DeferCleanup(k8sClient.Delete, ctx, configMap)
}
func simulateCentralReadyCount(designateName types.NamespacedName, readyCount int32) {
	Eventually(func(g Gomega) {
		designate := GetDesignate(designateName)
		designate.Status.DesignateCentralReadyCount = readyCount
		g.Expect(th.K8sClient.Status().Update(th.Ctx, designate)).To(Succeed())
	}, th.Timeout, th.Interval).Should(Succeed())
}

func createAndSimulateDB(spec map[string]interface{}) {
	DeferCleanup(
		mariadb.DeleteDBService,
		mariadb.CreateDBService(
			namespace,
			spec["databaseInstance"].(string),
			corev1.ServiceSpec{
				Ports: []corev1.ServicePort{{Port: 3306}},
			},
		),
	)
	mariadb.CreateMariaDBAccount(namespace, spec["databaseAccount"].(string), mariadbv1.MariaDBAccountSpec{
		Secret:   "osp-secret",
		UserName: "designate",
	})
	mariadb.CreateMariaDBDatabase(namespace, designate.DatabaseCRName, mariadbv1.MariaDBDatabaseSpec{})
	mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: spec["databaseAccount"].(string)})
	mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: designate.DatabaseCRName})
}

func createAndSimulateBind9(bind9Name types.NamespacedName) {
	bind9Spec := GetDefaultDesignateBackendbind9Spec()
	DeferCleanup(k8sClient.Delete, ctx, CreateDesignateBackendbind9(bind9Name, bind9Spec))
}

func createAndSimulateMdns(mdnsName types.NamespacedName) {
	mdnsSpec := GetDefaultDesignateMdnsSpec()
	DeferCleanup(k8sClient.Delete, ctx, CreateDesignateMdns(mdnsName, mdnsSpec))
}

var _ = Describe("Designate controller", func() {
	var name string
	var spec map[string]interface{}
	var designateName types.NamespacedName
	var designateBind9Name types.NamespacedName
	var designateMdnsName types.NamespacedName
	var designateNSRecordConfigMapName types.NamespacedName
	var transportURLName types.NamespacedName
	var transportURLSecretName types.NamespacedName
	var designateDBSyncName types.NamespacedName
	var designateRedisName types.NamespacedName
	var bind9ReplicaCount int
	var mdnsReplicaCount int

	BeforeEach(func() {
		name = fmt.Sprintf("designate-%s", uuid.New().String())
		bind9ReplicaCount = rand.Intn(5) + 1
		mdnsReplicaCount = rand.Intn(5) + 1
		spec = GetDefaultDesignateSpec(bind9ReplicaCount, mdnsReplicaCount)

		designateName = types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		}

		transportURLName = types.NamespacedName{
			Namespace: namespace,
			Name:      name + "-designate-transport",
		}

		transportURLSecretName = types.NamespacedName{
			Namespace: namespace,
			Name:      RabbitmqSecretName,
		}

		designateDBSyncName = types.NamespacedName{
			Namespace: namespace,
			Name:      designateName.Name + "-db-sync",
		}

		designateRedisName = types.NamespacedName{
			Namespace: namespace,
			Name:      "designate-redis",
		}

		designateBind9Name = types.NamespacedName{
			Namespace: namespace,
			Name:      fmt.Sprintf("designate-bind9-%s", uuid.New().String()),
		}

		designateMdnsName = types.NamespacedName{
			Namespace: namespace,
			Name:      fmt.Sprintf("designate-mdns-%s", uuid.New().String()),
		}

		designateNSRecordConfigMapName = types.NamespacedName{
			Namespace: namespace,
			Name:      designate.NsRecordsConfigMap,
		}
	})

	When("a Designate instance is created", func() {
		BeforeEach(func() {
			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
		})

		It("should have the Spec fields initialized", func() {
			designate := GetDesignate(designateName)
			Expect(designate.Spec.DatabaseInstance).Should(Equal("test-designate-db-instance"))
			Expect(designate.Spec.Secret).Should(Equal(SecretName))
		})

		It("should have the Status fields initialized", func() {
			designate := GetDesignate(designateName)
			Expect(designate.Status.DatabaseHostname).Should(Equal(""))
			Expect(designate.Status.TransportURLSecret).Should(Equal(""))
			Expect(designate.Status.DesignateAPIReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateCentralReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateWorkerReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateMdnsReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateProducerReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateBackendbind9ReadyCount).Should(Equal(int32(0)))
			Expect(designate.Status.DesignateUnboundReadyCount).Should(Equal(int32(0)))
		})

		It("should have Unknown Conditions initialized as TransportUrl not created", func() {
			for _, cond := range []condition.Type{
				condition.RabbitMqTransportURLReadyCondition,
				condition.DBReadyCondition,
				condition.ServiceConfigReadyCondition,
			} {
				th.ExpectCondition(
					designateName,
					ConditionGetterFunc(DesignateConditionGetter),
					cond,
					corev1.ConditionUnknown,
				)
			}
			// TODO(oschwart) InputReadyCondition is set to False while the controller is waiting for the transportURL to be created, this is probably not the correct behavior
			for _, cond := range []condition.Type{
				condition.InputReadyCondition,
				condition.ReadyCondition,
			} {
				th.ExpectCondition(
					designateName,
					ConditionGetterFunc(DesignateConditionGetter),
					cond,
					corev1.ConditionFalse,
				)
			}
		})

		It("should have a finalizer", func() {
			// the reconciler loop adds the finalizer so we have to wait for
			// it to run
			Eventually(func() []string {
				return GetDesignate(designateName).Finalizers
			}, timeout, interval).Should(ContainElement("openstack.org/designate"))
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateName.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// TransportURL
	When("a proper secret is provider, TransportURL is created", func() {
		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)
			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
		})

		It("should be in state of having the input ready", func() {
			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.InputReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should be in state of having the TransportURL ready", func() {
			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.RabbitMqTransportURLReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should not create a secret", func() {
			secret := types.NamespacedName{
				Namespace: designateName.Namespace,
				Name:      fmt.Sprintf("%s-%s", designateName.Name, "config-data"),
			}
			th.AssertSecretDoesNotExist(secret)
		})
	})

	// NAD
	// TODO

	// DB
	When("DB is created", func() {
		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)
			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
			DeferCleanup(
				mariadb.DeleteDBService,
				mariadb.CreateDBService(
					namespace,
					GetDesignate(designateName).Spec.DatabaseInstance,
					corev1.ServiceSpec{
						Ports: []corev1.ServicePort{{Port: 3306}},
					},
				),
			)
		})

		It("should set DBReady Condition and set DatabaseHostname Status", func() {
			mariadb.SimulateMariaDBAccountCompleted(types.NamespacedName{Namespace: namespace, Name: GetDesignate(designateName).Spec.DatabaseAccount})
			mariadb.SimulateMariaDBDatabaseCompleted(types.NamespacedName{Namespace: namespace, Name: designate.DatabaseCRName})
			th.SimulateJobSuccess(designateDBSyncName)
			designate := GetDesignate(designateName)
			hostname := "hostname-for-" + designate.Spec.DatabaseInstance + "." + namespace + ".svc"
			Expect(designate.Status.DatabaseHostname).To(Equal(hostname))

			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.DBReadyCondition,
				corev1.ConditionTrue,
			)

			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.DBSyncReadyCondition,
				corev1.ConditionFalse,
			)
		})
	})

	// Config
	When("The Config Secrets are created", func() {

		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)

			createAndSimulateDB(spec)
			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
				Namespace: namespace,
			}))

			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))

			th.SimulateJobSuccess(designateDBSyncName)
		})

		It("should set Service Config Ready Condition", func() {
			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.ServiceConfigReadyCondition,
				corev1.ConditionTrue,
			)
		})

		It("should create the designate.conf file in a Secret", func() {
			instance := GetDesignate(designateName)

			configData := th.GetSecret(
				types.NamespacedName{
					Namespace: designateName.Namespace,
					Name:      fmt.Sprintf("%s-config-data", designateName.Name)})
			Expect(configData).ShouldNot(BeNil())
			conf := string(configData.Data["designate.conf"])
			Expect(conf).Should(
				ContainSubstring(
					fmt.Sprintf(
						"username=%s\n",
						instance.Spec.ServiceUser)))

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
							instance.Status.DatabaseHostname,
							db.Name)))
			}
		})

		It("should create a Secret for the scripts", func() {
			scriptData := th.GetSecret(
				types.NamespacedName{
					Namespace: designateName.Namespace,
					Name:      fmt.Sprintf("%s-scripts", designateName.Name)})
			Expect(scriptData).ShouldNot(BeNil())
		})
	})

	// Networks Annotation
	When("Network Annotation is created", func() {
		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)

			createAndSimulateDB(spec)

			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
				Namespace: namespace,
			}))

			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
		})

		It("should set the NetworkAttachementReady condition", func() {
			th.ExpectCondition(
				designateName,
				ConditionGetterFunc(DesignateConditionGetter),
				condition.NetworkAttachmentsReadyCondition,
				corev1.ConditionTrue,
			)
		})
	})

	// API Deployment
	When("Designate is created with nodeSelector", func() {
		BeforeEach(func() {
			spec["nodeSelector"] = map[string]interface{}{
				"foo": "bar",
			}
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)
			createAndSimulateDB(spec)

			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
				Namespace: namespace,
			}))

			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))

			th.SimulateJobSuccess(designateDBSyncName)

			// TODO: assert nodeSelector on more resources when supported
		})

		It("sets nodeSelector in resource specs", func() {
			Eventually(func(g Gomega) {
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"foo": "bar"}))
			}, timeout, interval).Should(Succeed())
		})

		It("updates nodeSelector in resource specs when changed", func() {
			Eventually(func(g Gomega) {
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"foo": "bar"}))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				designate := GetDesignate(designateName)
				newNodeSelector := map[string]string{
					"foo2": "bar2",
				}
				designate.Spec.NodeSelector = &newNodeSelector
				g.Expect(k8sClient.Update(ctx, designate)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				th.SimulateJobSuccess(designateDBSyncName)
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"foo2": "bar2"}))
			}, timeout, interval).Should(Succeed())
		})

		It("removes nodeSelector from resource specs when cleared", func() {
			Eventually(func(g Gomega) {
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"foo": "bar"}))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				designate := GetDesignate(designateName)
				emptyNodeSelector := map[string]string{}
				designate.Spec.NodeSelector = &emptyNodeSelector
				g.Expect(k8sClient.Update(ctx, designate)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				th.SimulateJobSuccess(designateDBSyncName)
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(BeNil())
			}, timeout, interval).Should(Succeed())
		})

		It("removes nodeSelector from resource specs when nilled", func() {
			Eventually(func(g Gomega) {
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"foo": "bar"}))
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				designate := GetDesignate(designateName)
				designate.Spec.NodeSelector = nil
				g.Expect(k8sClient.Update(ctx, designate)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			Eventually(func(g Gomega) {
				th.SimulateJobSuccess(designateDBSyncName)
				g.Expect(th.GetJob(designateDBSyncName).Spec.Template.Spec.NodeSelector).To(BeNil())
			}, timeout, interval).Should(Succeed())
		})
	})

	When("Designate Bind9 and Mdns services are created", func() {
		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)
			createAndSimulateDB(spec)

			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
				Namespace: namespace,
			}))
			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
			th.SimulateJobSuccess(designateDBSyncName)

			createAndSimulateBind9(designateBind9Name)
			createAndSimulateMdns(designateMdnsName)
		})

		It("should have Unknown Conditions initialized for Designate services conditions initially", func() {
			for _, cond := range []condition.Type{
				designatev1.DesignateAPIReadyCondition,
				designatev1.DesignateBackendbind9ReadyCondition,
				designatev1.DesignateCentralReadyCondition,
				designatev1.DesignateMdnsReadyCondition,
				designatev1.DesignateProducerReadyCondition,
				designatev1.DesignateUnboundReadyCondition,
				designatev1.DesignateWorkerReadyCondition,
			} {
				th.ExpectCondition(
					designateName,
					ConditionGetterFunc(DesignateConditionGetter),
					cond,
					corev1.ConditionUnknown,
				)
			}
		})

		It("should create ConfigMaps for Bind9 and Mdns predictable IPs", func() {
			bindConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.BindPredIPConfigMap,
				Namespace: namespace})
			Expect(len(bindConfigMap.Data)).To(Equal(bind9ReplicaCount))

			usedIPs := make(map[string]bool)
			for key, ipAddress := range bindConfigMap.Data {
				// verify key with bind_address_N format
				Expect(key).To(MatchRegexp(`^bind_address_\d+$`))

				// verify valid IP format
				ip := net.ParseIP(ipAddress)
				Expect(ip).NotTo(BeNil(), "Invalid IP format: %s", ipAddress)

				// check there are no duplicate IPs
				Expect(usedIPs[ipAddress]).To(BeFalse(), "Duplicate IP found: %s", ipAddress)
				usedIPs[ipAddress] = true
			}

			mdnsConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.MdnsPredIPConfigMap,
				Namespace: namespace})
			Expect(len(mdnsConfigMap.Data)).To(Equal(mdnsReplicaCount))
			for key, ipAddress := range mdnsConfigMap.Data {
				// verify key with mdns_address_N format
				Expect(key).To(MatchRegexp(`^mdns_address_\d+$`))

				// verify valid IP format
				ip := net.ParseIP(ipAddress)
				Expect(ip).NotTo(BeNil(), "Invalid IP format: %s", ipAddress)

				// check there are no duplicate IPs
				Expect(usedIPs[ipAddress]).To(BeFalse(), "Duplicate IP found: %s", ipAddress)
				usedIPs[ipAddress] = true
			}
		})
	})

	When("Designate ns_records ConfigMap is created", func() {
		BeforeEach(func() {
			createAndSimulateKeystone(designateName)
			createAndSimulateRedis(designateRedisName)
			createAndSimulateDesignateSecrets(designateName)
			createAndSimulateTransportURL(transportURLName, transportURLSecretName)
			createAndSimulateDB(spec)

			DeferCleanup(k8sClient.Delete, ctx, CreateNAD(types.NamespacedName{
				Name:      spec["designateNetworkAttachment"].(string),
				Namespace: namespace,
			}))
			DeferCleanup(th.DeleteInstance, CreateDesignate(designateName, spec))
			th.SimulateJobSuccess(designateDBSyncName)

			createAndSimulateBind9(designateBind9Name)
			createAndSimulateMdns(designateMdnsName)
			createAndSimulateNSRecordsConfigMap(designateNSRecordConfigMapName)
			simulateCentralReadyCount(designateName, 1)
		})

		It("should have created a valid pools.yaml configmap", func() {
			nsRecordsConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.NsRecordsConfigMap,
				Namespace: namespace})
			Expect(nsRecordsConfigMap).ToNot(BeNil())

			poolsYamlConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.PoolsYamlConfigMap,
				Namespace: namespace})
			Expect(poolsYamlConfigMap).ToNot(BeNil())

			var pools []designate.Pool
			err := yaml.Unmarshal([]byte(poolsYamlConfigMap.Data[designate.PoolsYamlContent]), &pools)
			Expect(err).ToNot(HaveOccurred())

			validate := validator.New()
			for _, pool := range pools {
				Expect(pool.Name).ToNot(BeEmpty(), "Pool name should not be an empty string")
				Expect(pool.Description).ToNot(BeEmpty(), "Pool description should not be an empty string")

				// Check attributes if exists
				if len(pool.Attributes) > 0 {
					for _, v := range pool.Attributes {
						Expect(v).ToNot(BeEmpty(), "Attribute value should not be an empty string")
					}
				}

				// Check NS records
				Expect(pool.NSRecords).ToNot(BeEmpty(), "NS records should not be empty")
				for _, ns := range pool.NSRecords {
					err := validate.Var(ns.Hostname, "fqdn")
					Expect(err).ToNot(HaveOccurred(), "NS record hostname should be valid FQDN")
					Expect(ns.Priority).To(BeNumerically(">", 0), "NS record priority should be a positive number")
				}

				// Check nameservers
				Expect(pool.Nameservers).ToNot(BeEmpty(), "Nameservers should not be empty")
				for _, ns := range pool.Nameservers {
					ip := net.ParseIP(ns.Host)
					Expect(ip).NotTo(BeNil(), "Nameserver host should be valid IP")
					Expect(ns.Port).To(Equal(53), "Nameserver port should be 53")
				}

				// Check targets
				Expect(pool.Targets).ToNot(BeEmpty(), "Targets should not be empty")
				var numOfBindHosts int
				for i, target := range pool.Targets {
					// Check target type (Designate Backend)
					Expect(target.Type).To(Equal("bind9"), "Only Bind9 is a supported Designate backend")

					// Check description format
					serverNum := i
					expectedDesc := fmt.Sprintf("BIND9 Server %d (%s)", serverNum, target.Options.Host)
					Expect(target.Description).To(Equal(expectedDesc), "Target description format mismatch")

					// Check masters
					Expect(target.Masters).To(HaveLen(mdnsReplicaCount), "Masters count should match mdnsReplicaCount")
					for _, master := range target.Masters {
						ip := net.ParseIP(master.Host)
						Expect(ip).NotTo(BeNil(), "Master host should be valid IP")
						Expect(master.Port).To(BeNumerically(">", 0), "Master port should be a positive number")
					}

					// Check options
					Expect(target.Options.Host).To(Equal(target.Options.RNDCHost), "Options Host and RNDCHost should match")

					// We can't know the order which the nameservers are stored, so we make sure they match the options
					// host and rndc_host. After this loop we assert the len(nameservers) == len(hosts)
					foundMatch := false
					for _, ns := range pool.Nameservers {
						if ns.Host == target.Options.Host {
							foundMatch = true
							numOfBindHosts++
							break
						}
					}
					Expect(foundMatch).To(BeTrue(), "Options Host should match one of the nameserver hosts")

					// Check options values
					Expect(target.Options.Port).To(Equal(53), "Options port should be 53")
					Expect(target.Options.RNDCPort).To(BeNumerically(">", 0), "RNDC port should be a positive number")

					// Validate RNDC config file path
					expectedRndcPath := fmt.Sprintf("/etc/designate/rndc-keys/rndc-key-%d", serverNum)
					Expect(target.Options.RNDCKeyFile).To(Equal(expectedRndcPath), "RNDC config file path mismatch")
				}

				// Validate len(nameservers) == len(hosts) - which are all Bind9 hosts
				Expect(numOfBindHosts).To(Equal(len(pool.Nameservers)))

				if pool.CatalogZone != nil {
					err := validate.Var(pool.CatalogZone.FQDN, "fqdn")
					Expect(err).ToNot(HaveOccurred())
					Expect(pool.CatalogZone.Refresh).To(BeNumerically(">", 0), "catalog_zone_refresh should be a positive number")
				}
			}
		})

		It("should create the same pools.yaml hash when provided the same designate configmaps", func() {
			bindConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.BindPredIPConfigMap,
				Namespace: namespace})
			mdnsConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.MdnsPredIPConfigMap,
				Namespace: namespace})
			nsRecordsConfigMap := th.GetConfigMap(types.NamespacedName{
				Name:      designate.NsRecordsConfigMap,
				Namespace: namespace})
			_, poolsYamlHash, err := designate.GeneratePoolsYamlDataAndHash(bindConfigMap.Data, mdnsConfigMap.Data, nsRecordsConfigMap.Data)
			Expect(err).ToNot(HaveOccurred())
			for i := 0; i < 10; i++ {
				_, newPoolsYamlHash, err := designate.GeneratePoolsYamlDataAndHash(bindConfigMap.Data, mdnsConfigMap.Data, nsRecordsConfigMap.Data)
				Expect(err).ToNot(HaveOccurred())
				Expect(poolsYamlHash).Should(Equal(newPoolsYamlHash))
			}
		})
	})
})
