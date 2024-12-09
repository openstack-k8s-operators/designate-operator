package designatebackendbind9

import (
	"fmt"
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	Bind9AppName = "designatebackendbind9"
)

// CreateBindServices creates a service for each bind9 pod
func CreateBindServices(m *designatev1beta1.DesignateBackendbind9, replicas int32, addressPool string) []*corev1.Service {
	services := make([]*corev1.Service, replicas)

	for i := int32(0); i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", m.Name, i)
		services[i] = createSingleBindService(m, podName, addressPool)
	}

	return services
}

func createSingleBindService(m *designatev1beta1.DesignateBackendbind9, podName string, addressPool string) *corev1.Service {
	labels := labels.GetLabels(m, Bind9AppName, map[string]string{
		"owner": "designate",
		"cr":    m.GetName(),
		"app":   Bind9AppName,
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
				*metav1.NewControllerRef(m, designatev1beta1.GroupVersion.WithKind(Bind9AppName)),
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeLoadBalancer,
			Selector: map[string]string{
				"app":                                "bind",
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
