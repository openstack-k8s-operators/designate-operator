/*

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

package designate

import (
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	corev1 "k8s.io/api/core/v1"
)

// CreateDNSService - helper function for creating a new serv
func CreateDNSService(
	name string,
	namespace string,
	details *service.OverrideSpec,
	labels map[string]string,
	port int32,
) (*service.Service, error) {
	if details.EmbeddedLabelsAnnotations == nil {
		details.EmbeddedLabelsAnnotations = &service.EmbeddedLabelsAnnotations{}
	}
	selector := make(map[string]string)
	// Service name is expected to match the name of the pod.
	selector["statefulset.kubernetes.io/pod-name"] = name
	ports := []corev1.ServicePort{
		{
			Name:     "dns",
			Port:     port,
			Protocol: corev1.ProtocolUDP,
		},
		{
			Name:     "dns-tcp",
			Port:     port,
			Protocol: corev1.ProtocolTCP,
		},
	}

	svc, err := service.NewService(
		service.GenericService(
			&service.GenericServiceDetails{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
				Selector:  selector,
				Ports:     ports,
			},
		),
		5,
		details,
	)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
