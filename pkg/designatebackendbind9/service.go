package designatebackendbind9

import (
	"context"
	"fmt"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

const (
	Bind9AppName = "designatebackendbind9"
)

// CreateBindServices creates a service for each bind9 pod
func CreateBindServices(h *helper.Helper, m *designatev1beta1.DesignateBackendbind9, replicas int32, addressPool string) ([]*corev1.Service, error) {
	services := make([]*corev1.Service, replicas)

	for i := int32(0); i < replicas; i++ {
		podName := fmt.Sprintf("%s-%d", m.Name, i)

		existingService := &corev1.Service{}
		err := h.GetClient().Get(context.Background(), types.NamespacedName{
			Name:      podName,
			Namespace: m.Namespace,
		}, existingService)

		if err != nil && !k8s_errors.IsNotFound(err) {
			return nil, err
		}
		if k8s_errors.IsNotFound(err) {
			services[i] = createSingleBindService(m, podName, addressPool)
		} else {
			services[i] = existingService
		}
	}

	return services, nil
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
					Name:     "named-tcp",
					Port:     53,
					Protocol: corev1.ProtocolTCP,
				},
				{
					Name:     "named-udp",
					Port:     53,
					Protocol: corev1.ProtocolUDP,
				},
			},
		},
	}
}
