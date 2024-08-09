package designatebackendbind9

import (
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	service "github.com/openstack-k8s-operators/lib-common/modules/common/service"
	corev1 "k8s.io/api/core/v1"
)

func Service(m *designatev1beta1.DesignateBackendbind9) *corev1.Service {
	labels := labels.GetLabels(m, "designatebackendbind9", map[string]string{
		"owner": "designate",
		"cr":    m.GetName(),
		"app":   "designatebackendbind9",
	})
	servicePorts := make([]corev1.ServicePort, 2)
	servicePorts[0] = corev1.ServicePort{
		Name:     "named",
		Port:     53,
		Protocol: corev1.ProtocolUDP,
	}
	servicePorts[1] = corev1.ServicePort{
		Name:     "named",
		Port:     53,
		Protocol: corev1.ProtocolTCP,
	}
	details := &service.GenericServiceDetails{
		Name:      m.GetName(),
		Namespace: m.GetNamespace(),
		Labels:    labels,
		Selector: map[string]string{
			"app": "bind",
		},
		Ports:     servicePorts,
		ClusterIP: "None",
	}

	svc := service.GenericService(details)
	return svc
}
