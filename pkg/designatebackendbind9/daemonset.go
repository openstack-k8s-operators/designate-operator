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
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"

	// labels "github.com/openstack-k8s-operators/lib-common/modules/common/labels"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/util/intstr"
)

const (
	// ServiceCommand -
	ServiceCommand = "/usr/local/bin/kolla_set_configs && /usr/local/bin/kolla_start"
)

// DaemonSet func
func DaemonSet(
	instance *designatev1beta1.DesignateBackendbind9,
	configHash string,
	labels map[string]string,
	annotations map[string]string,
) *appsv1.DaemonSet {
	rootUser := int64(0)
	// Designate's uid and gid magic numbers come from the 'designate-user' in
	// https://github.com/openstack/kolla/blob/master/kolla/common/users.py
	// designateUser := int64(42411)
	// designateGroup := int64(42411)

	livenessProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      15,
		PeriodSeconds:       13,
		InitialDelaySeconds: 3,
	}
	startupProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      15,
		PeriodSeconds:       15,
		InitialDelaySeconds: 5,
	}
	livenessProbe.Exec = &corev1.ExecAction{
		Command: []string{
			"/usr/bin/pgrep", "-r", "DRST", "-f", "named",
		},
	}
	startupProbe.Exec = livenessProbe.Exec

	// // TODO(beagles) this can be simplified - jumped through a few hops to
	// // avoid interfering with development in progress.
	// args := []string{}
	// command := []string{}
	// // XXX(beagles) forced because orig was forced.
	// if true {
	// 	command = append(command, "/bin/sleep", "60000")
	// 	livenessProbe.Exec = &corev1.ExecAction{
	// 		Command: []string{
	// 			"/bin/true",
	// 		},
	// 	}
	// 	readinessProbe.Exec = livenessProbe.Exec
	// } else {
	// 	command = append(command, "/bin/bash")
	// 	args = append(args, "-c", ServiceCommand)

	// 	// Check for the rndc port.
	// 	livenessProbe.TCPSocket = &corev1.TCPSocketAction{
	// 		Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(953)},
	// 	}
	// 	readinessProbe.TCPSocket = &corev1.TCPSocketAction{
	// 		Port: intstr.IntOrString{Type: intstr.Int, IntVal: int32(953)},
	// 	}
	// }

	envVars := map[string]env.Setter{}
	envVars["KOLLA_CONFIG_STRATEGY"] = env.SetValue("COPY_ALWAYS")
	envVars["CONFIG_HASH"] = env.SetValue(configHash)

	daemonset := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
					Labels:      labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: instance.Spec.ServiceAccount,
					Volumes: designate.GetVolumes(
						designate.GetOwningDesignateName(instance),
					),
					Containers: []corev1.Container{
						{
							Name: designate.ServiceName + "-backendbind9",
							// NOTE: dkehn@redhat.com This sleep is to hold
							// the pod until the bind9 is manually setup
							Command: []string{
								"/bin/sleep",
								"600000",
								// Command: []string{
								// 	"/bin/bash",
							},
							// Args:  args,
							Image: instance.Spec.ContainerImage,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: &rootUser,
							},
							Env:           env.MergeEnvs([]corev1.EnvVar{}, envVars),
							VolumeMounts:  designate.GetServiceVolumeMounts("designate-backendbind9"),
							Resources:     instance.Spec.Resources,
							StartupProbe:  startupProbe,
							LivenessProbe: livenessProbe,
						},
					},
					NodeSelector: instance.Spec.NodeSelector,
				},
			},
		},
	}

	// If possible two pods of the same service should not
	// run on the same worker node. If this is not possible
	// the get still created on the same worker node.
	daemonset.Spec.Template.Spec.Affinity = affinity.DistributePods(
		common.AppSelector,
		[]string{
			designate.ServiceName,
		},
		corev1.LabelHostname,
	)
	if instance.Spec.NodeSelector != nil && len(instance.Spec.NodeSelector) > 0 {
		daemonset.Spec.Template.Spec.NodeSelector = instance.Spec.NodeSelector
	}

	initContainerDetails := designate.APIDetails{
		ContainerImage:       instance.Spec.ContainerImage,
		DatabaseHost:         instance.Spec.DatabaseHostname,
		DatabaseName:         designate.DatabaseName,
		OSPSecret:            instance.Spec.Secret,
		TransportURLSecret:   instance.Spec.TransportURLSecret,
		UserPasswordSelector: instance.Spec.PasswordSelectors.Service,
		VolumeMounts:         designate.GetInitVolumeMounts(),
	}
	daemonset.Spec.Template.Spec.InitContainers = designate.InitContainer(initContainerDetails)

	return daemonset
}
