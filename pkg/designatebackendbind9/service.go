package designatebackendbind9

import (
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	service "github.com/openstack-k8s-operators/lib-common/modules/common/service"
	corev1 "k8s.io/api/core/v1"
)

// Service exposes DesignateBackendbind9 pods for a designate CR
func Service(m *designatev1beta1.DesignateBackendbind9) *corev1.Service {
	labels := labels.GetLabels(m, "designatebackendbind9", map[string]string{
		"owner": "infra-operator",
		"cr":    m.GetName(),
		"app":   "designatebackendbind9",
	})
	details := &service.GenericServiceDetails{
		Name:      m.GetName(),
		Namespace: m.GetNamespace(),
		Labels:    labels,
		Selector: map[string]string{
			"app": "designatebackendbind9",
		},
		Port: service.GenericServicePort{
			Name:     "designatebackendbind9",
			Port:     53,
			Protocol: "TCP",
		},
		ClusterIP: "None",
	}

	svc := service.GenericService(details)
	return svc
}
