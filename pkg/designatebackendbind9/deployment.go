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

package designatebackendbind9

import (
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	designate "github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	// common "github.com/openstack-k8s-operators/lib-common/modules/common"
	// "github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"

	labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// ServiceCommand -
	ServiceCommand = "/usr/local/bin/kolla_set_configs && /usr/local/bin/kolla_start"
)

// Deployment func
func Deployment(instance *designatev1beta1.DesignateBackendbind9) *appsv1.Deployment {
	matchls := map[string]string{
		"app":   "designaatebackendbind9",
		"cr":    "designatebackendbind9-" + instance.Name,
		"owner": "designate-operator",
	}
	ls := labels.GetLabels(instance, "designatebackendbind9", matchls)

	// livenessProbe := &corev1.Probe{
	// 	// TODO might need tuning
	// 	TimeoutSeconds:      5,
	// 	PeriodSeconds:       3,
	// 	InitialDelaySeconds: 3,
	// }
	// readinessProbe := &corev1.Probe{
	// 	// TODO might need tuning
	// 	TimeoutSeconds:      5,
	// 	PeriodSeconds:       5,
	// 	InitialDelaySeconds: 5,
	// }

	// TODO might want to disable probes in 'Debug' mode
	// livenessProbe.TCPSocket = &corev1.TCPSocketAction{
	// 	Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(6379)},
	// }
	// readinessProbe.TCPSocket = &corev1.TCPSocketAction{
	// 	Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(6379)},
	// }

	envVars := map[string]env.Setter{}
	envVars["KOLLA_CONFIG_FILE"] = env.SetValue(KollaConfig)
	envVars["KOLLA_CONFIG_STRATEGY"] = env.SetValue("COPY_ALWAYS")
	// envVars["CONFIG_HASH"] = env.SetValue(configHash)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: instance.Spec.Replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: matchls,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: ls,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: instance.RbacResourceName(),
					Containers: []corev1.Container{{
						Image: instance.Spec.ContainerImage,
						Name:  "designatebackendbind9",
						Command: []string{
							"/bin/sleep", "60000",
						},
						// Command: []string{
						// 	"/bin/bash",
						// },
						// Args:  args,
						Ports: []corev1.ContainerPort{{
							ContainerPort: 6379,
							Name:          "designatebackendbind9",
						}},
						Env:          env.MergeEnvs([]corev1.EnvVar{}, envVars),
						VolumeMounts: designate.GetServiceVolumeMounts("designate-backendbind9"),
						Resources:    instance.Spec.Resources,
						// ReadinessProbe: readinessProbe,
						// LivenessProbe:  livenessProbe,
					}},
				},
			},
		},
	}

	return deployment
}
