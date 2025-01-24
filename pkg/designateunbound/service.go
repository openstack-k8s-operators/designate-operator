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

package designateunbound

import (
	"fmt"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	UnboundAppName = "designateunbound"
)

// CreateUnboundServices creates a service for each unbound pod
func CreateUnboundServices(m *designatev1beta1.DesignateUnbound, replicas int32, addressPool string) []*corev1.Service {
	services := make([]*corev1.Service, replicas)

	for i := int32(0); i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", m.Name, i)
		services[i] = createSingleUnboundService(m, podName, addressPool)
	}

	return services
}

func createSingleUnboundService(m *designatev1beta1.DesignateUnbound, podName string, addressPool string) *corev1.Service {
	labels := labels.GetLabels(m, UnboundAppName, map[string]string{
		"owner": "designate",
		"cr":    m.GetName(),
		"app":   UnboundAppName,
	})

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.Namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"metallb.universe.tf/address-pool": addressPool,
			},
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(m, designatev1beta1.GroupVersion.WithKind(UnboundAppName)),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{
				"app":                                "unbound",
				"statefulset.kubernetes.io/pod-name": podName,
			},
			Ports: []corev1.ServicePort{
				{
					Name:     "named-udp",
					Port:     53,
					Protocol: corev1.ProtocolUDP,
				},
				{
					Name:     "named-tcp",
					Port:     53,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
}
