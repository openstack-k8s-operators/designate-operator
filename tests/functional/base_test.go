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
	"encoding/json"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	rabbitmqv1 "github.com/openstack-k8s-operators/infra-operator/apis/rabbitmq/v1beta1"
	redisv1 "github.com/openstack-k8s-operators/infra-operator/apis/redis/v1beta1"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/lib-common/modules/common/endpoint"
)

const (
	SecretName         = "test-secret"
	KeystoneSecretName = "%s-keystone-secret"
	RabbitmqSecretName = "rabbitmq-secret"

	PublicCertSecretName   = "public-tls-certs"
	InternalCertSecretName = "internal-tls-certs"
	CABundleSecretName     = "combined-ca-bundle"

	timeout  = time.Second * 5
	interval = timeout / 100
)

func CreateTransportURL(name types.NamespacedName) *rabbitmqv1.TransportURL {
	transportURL := &rabbitmqv1.TransportURL{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name.Name,
			Namespace: name.Namespace,
		},
		Spec: rabbitmqv1.TransportURLSpec{
			RabbitmqClusterName: "rabbitmq",
		},
	}
	Expect(k8sClient.Create(ctx, transportURL)).Should(Succeed())
	return infra.GetTransportURL(name)
}

func CreateTransportURLSecret(name types.NamespacedName) *corev1.Secret {
	secret := th.CreateSecret(
		name,
		map[string][]byte{
			"transport_url": []byte(fmt.Sprintf("rabbit://%s/", name)),
		},
	)
	logger.Info("Created TransportURLSecret", "secret", secret)
	return secret
}

func createAndSimulateRedis(name types.NamespacedName) {
	replicas := int32(1)
	redis := &redisv1.Redis{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "designate-redis",
			Namespace: name.Namespace,
		},
		Spec: redisv1.RedisSpec{
			RedisSpecCore: redisv1.RedisSpecCore{
				Replicas: &replicas,
			},
			ContainerImage: "repo/redis-image",
		},
	}
	Expect(k8sClient.Create(ctx, redis)).Should(Succeed())
	DeferCleanup(k8sClient.Delete, ctx, redis)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "designate-redis",
			Namespace: name.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name: "redis",
					Port: 6379,
				},
			},
		},
	}
	Expect(k8sClient.Create(ctx, svc)).Should(Succeed())
	DeferCleanup(k8sClient.Delete, ctx, svc)
}

func SimulateKeystoneReady(
	name types.NamespacedName,
	publicEndpointURL string,
	internalEndpointURL string,
) {
	secretName := fmt.Sprintf(KeystoneSecretName, name.Name)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"admin-password": []byte("12345678"),
		},
	}
	Expect(k8sClient.Create(ctx, secret)).Should(Succeed())
	DeferCleanup(k8sClient.Delete, ctx, secret)
	Eventually(func(g Gomega) {
		ks := keystone.GetKeystoneAPI(name)
		ks.Spec.Secret = secretName
		ks.Spec.Region = "RegionOne"
		ks.Spec.AdminProject = "admin"
		g.Expect(k8sClient.Update(ctx, ks)).To(Succeed())
		ks.Status.APIEndpoints[string(endpoint.EndpointInternal)] = internalEndpointURL
		ks.Status.APIEndpoints[string(endpoint.EndpointPublic)] = publicEndpointURL
		g.Expect(k8sClient.Status().Update(ctx, ks)).To(Succeed())
	}, timeout, interval).Should(Succeed())
}

func GetDefaultDesignateSpec(bind9ReplicaCount, mdnsReplicaCount int) map[string]interface{} {
	spec := map[string]interface{}{
		"databaseInstance":           "test-designate-db-instance",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"databaseAccount":            "designate-db-account",
	}
	spec["designateBackendbind9"] = designatev1.DesignateBackendbind9Spec{
		DesignateBackendbind9SpecBase: designatev1.DesignateBackendbind9SpecBase{
			Replicas: ptr.To(int32(bind9ReplicaCount)),
		},
	}
	spec["designateMdns"] = designatev1.DesignateMdnsSpec{
		DesignateMdnsSpecBase: designatev1.DesignateMdnsSpecBase{
			Replicas: ptr.To(int32(mdnsReplicaCount)),
		},
	}
	return spec
}

func CreateDesignate(name types.NamespacedName, spec map[string]interface{}) client.Object {

	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "Designate",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignate(name types.NamespacedName) *designatev1.Designate {
	instance := &designatev1.Designate{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func DesignateConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignate(name)
	return instance.Status.Conditions
}

func CreateDesignateSecret(namespace string) *corev1.Secret {
	secret := th.CreateSecret(
		types.NamespacedName{Namespace: namespace, Name: SecretName},
		map[string][]byte{
			"DesignatePassword": []byte("DesignatePassword12345678"),
		},
	)
	logger.Info("Secret created", "name", SecretName, "namespace", namespace)
	return secret
}

// DesignateAPI
func GetDefaultDesignateAPISpec() map[string]interface{} {
	return map[string]interface{}{
		"databaseHostname":           "hostname-for-designate-api",
		"databaseInstance":           "test-designate-db-instance",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"containerImage":             "repo/designate-api-image",
		"serviceAccount":             "designate",
	}
}

func CreateDesignateAPI(name types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "DesignateAPI",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignateAPI(name types.NamespacedName) *designatev1.DesignateAPI {
	instance := &designatev1.DesignateAPI{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func GetDesignateAPISpec(name types.NamespacedName) designatev1.DesignateAPISpec {
	instance := &designatev1.DesignateAPI{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance.Spec
}

func GetDesignateCentralSpec(name types.NamespacedName) designatev1.DesignateCentralSpec {
	instance := &designatev1.DesignateCentral{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance.Spec
}

func GetDesignateProducerSpec(name types.NamespacedName) designatev1.DesignateProducerSpec {
	instance := &designatev1.DesignateProducer{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance.Spec
}

func DesignateAPIConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignateAPI(name)
	return instance.Status.Conditions
}

func SimulateDesignateAPIReady(name types.NamespacedName) {
	Eventually(func(g Gomega) {
		designateAPI := GetDesignateAPI(name)
		designateAPI.Status.ObservedGeneration = designateAPI.Generation
		designateAPI.Status.ReadyCount = 1
		g.Expect(k8sClient.Status().Update(ctx, designateAPI)).To(Succeed())
	}, timeout, interval).Should(Succeed())
}

// DesignateBackendbind9
func GetDefaultDesignateBackendbind9Spec() map[string]interface{} {
	return map[string]interface{}{
		"databaseHostname":           "hostname-for-designate-backendbind9",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"containerImage":             "repo/designate-backendbind9-image",
		"serviceAccount":             "designate",
	}
}

func CreateDesignateBackendbind9(name types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "DesignateBackendbind9",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignateBackendbind9(name types.NamespacedName) *designatev1.DesignateBackendbind9 {
	instance := &designatev1.DesignateBackendbind9{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func DesignateBackendbind9ConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignateBackendbind9(name)
	return instance.Status.Conditions
}

// DesignateMdns
func GetDefaultDesignateMdnsSpec() map[string]interface{} {
	return map[string]interface{}{
		"databaseHostname":           "hostname-for-designate-mdns",
		"databaseInstance":           "test-designate-db-instance",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"containerImage":             "repo/designate-mdns-image",
		"serviceAccount":             "designate",
	}
}

func CreateDesignateMdns(name types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "DesignateMdns",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignateMdns(name types.NamespacedName) *designatev1.DesignateMdns {
	instance := &designatev1.DesignateMdns{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func DesignateMdnsConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignateMdns(name)
	return instance.Status.Conditions
}

// DesignateCentral
func GetDefaultDesignateCentralSpec() map[string]interface{} {
	return map[string]interface{}{
		"databaseHostname":           "hostname-for-designate-central",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"containerImage":             "repo/designate-central-image",
		"serviceAccount":             "designate",
	}
}

func CreateDesignateCentral(name types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "DesignateCentral",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignateCentral(name types.NamespacedName) *designatev1.DesignateCentral {
	instance := &designatev1.DesignateCentral{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func GetDesignateWorker(name types.NamespacedName) *designatev1.DesignateWorker {
	instance := &designatev1.DesignateWorker{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func DesignateCentralConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignateCentral(name)
	return instance.Status.Conditions
}

// DesignateProducer
func GetDefaultDesignateProducerSpec() map[string]interface{} {
	return map[string]interface{}{
		"databaseHostname":           "hostname-for-designate-producer",
		"secret":                     SecretName,
		"designateNetworkAttachment": "designate-attachement",
		"containerImage":             "repo/designate-producer-image",
		"serviceAccount":             "designate",
	}
}

func CreateDesignateProducer(name types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "designate.openstack.org/v1beta1",
		"kind":       "DesignateProducer",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetDesignateProducer(name types.NamespacedName) *designatev1.DesignateProducer {
	instance := &designatev1.DesignateProducer{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func DesignateProducerConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetDesignateProducer(name)
	return instance.Status.Conditions
}

// Network attachment
func CreateNAD(name types.NamespacedName) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "k8s.cni.cncf.io/v1",
		"kind":       "NetworkAttachmentDefinition",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": map[string]interface{}{
			"config": `{
				"cniVersion": "0.3.1",
				"name": "designate",
				"type": "bridge",
				"ipam": {
					"type": "whereabouts",
					"range": "172.28.0.0/24",
					"range_start": "172.28.0.30",
					"range_end": "172.28.0.70"
				}
			}`,
		},
	}
	return th.CreateUnstructured(raw)
}

func GetNADConfig(name types.NamespacedName) *designate.NADConfig {
	nad := &networkv1.NetworkAttachmentDefinition{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, nad)).Should(Succeed())
	}, timeout, interval).Should(Succeed())

	nadConfig := &designate.NADConfig{}
	jsonDoc := []byte(nad.Spec.Config)
	err := json.Unmarshal(jsonDoc, nadConfig)
	if err != nil {
		return nil
	}

	return nadConfig
}

func CreateNode(name types.NamespacedName) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]interface{}{
			"name":      name.Name,
			"namespace": name.Namespace,
		},
		"spec": map[string]interface{}{},
	}
	return th.CreateUnstructured(raw)
}

func CreateDesignateNSRecordsConfigMap(name types.NamespacedName) client.Object {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      designate.NsRecordsConfigMap,
			Namespace: name.Namespace,
		},
		Data: map[string]string{
			"ns_records": `- hostname: ns1.example.com.
  priority: 1
- hostname: ns2.example.com.
  priority: 2`,
		},
	}
}

// GetSampleTopologySpec - An opinionated Topology Spec sample used to
// test Service components. It returns both the user input representation
// in the form of map[string]string, and the Golang expected representation
// used in the test asserts.
func GetSampleTopologySpec(label string) (map[string]interface{}, []corev1.TopologySpreadConstraint) {
	// Build the topology Spec
	topologySpec := map[string]interface{}{
		"topologySpreadConstraints": []map[string]interface{}{
			{
				"maxSkew":           1,
				"topologyKey":       corev1.LabelHostname,
				"whenUnsatisfiable": "ScheduleAnyway",
				"labelSelector": map[string]interface{}{
					"matchLabels": map[string]interface{}{
						"component": label,
					},
				},
			},
		},
	}
	// Build the topologyObj representation
	topologySpecObj := []corev1.TopologySpreadConstraint{
		{
			MaxSkew:           1,
			TopologyKey:       corev1.LabelHostname,
			WhenUnsatisfiable: corev1.ScheduleAnyway,
			LabelSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"component": label,
				},
			},
		},
	}
	return topologySpec, topologySpecObj
}

// CreateTopology - Creates a Topology CR based on the spec passed as input
func CreateTopology(topology types.NamespacedName, spec map[string]interface{}) client.Object {
	raw := map[string]interface{}{
		"apiVersion": "topology.openstack.org/v1beta1",
		"kind":       "Topology",
		"metadata": map[string]interface{}{
			"name":      topology.Name,
			"namespace": topology.Namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

// GetTopology - Returns the referenced Topology
func GetTopology(name types.NamespacedName) *topologyv1.Topology {
	instance := &topologyv1.Topology{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}
