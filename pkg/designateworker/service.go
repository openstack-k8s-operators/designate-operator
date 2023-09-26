package designateworker

import (
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	service "github.com/openstack-k8s-operators/lib-common/modules/common/service"
	corev1 "k8s.io/api/core/v1"
)

// Service exposes DesignateWorker pods for a designate CR
func Service(m *designatev1beta1.DesignateWorker) *corev1.Service {
	instance := &designatev1beta1.DesignateWorker{}
	var protocol corev1.Protocol
	protocol = corev1.ProtocolUDP
	if instance.Spec.BackendMdnsServerProtocol == "json:TCP" {
		protocol = corev1.ProtocolTCP
	}

	labels := labels.GetLabels(m, "designateworker", map[string]string{
		"owner": "designate",
		"cr":    m.GetName(),
		"app":   "designateworker",
	})
	details := &service.GenericServiceDetails{
		Name:      m.GetName(),
		Namespace: m.GetNamespace(),
		Labels:    labels,
		Selector: map[string]string{
			"app": "notify",
		},
		Port: service.GenericServicePort{
			Name:     "notifyUDP",
			Port:     53,
			Protocol: protocol,
		},
		ClusterIP: "None",
	}

	svc := service.GenericService(details)
	return svc
}
