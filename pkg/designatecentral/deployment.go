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

package designatecentral

import (
	"fmt"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	designate "github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/util/intstr"
)

// Deployment func
func Deployment(
	instance *designatev1beta1.DesignateCentral,
	configHash string,
	labels map[string]string,
	annotations map[string]string,
) *appsv1.Deployment {
	rootUser := int64(0)
	// Designate's uid and gid magic numbers come from the 'designate-user' in
	// https://github.com/openstack/kolla/blob/master/kolla/common/users.py
	// designateUser := int64(42411)
	// designateGroup := int64(42411)

	serviceName := fmt.Sprintf("%s-central", designate.ServiceName)
	volumes := getServicePodVolumes(serviceName)
	volumeMounts := getServicePodVolumeMounts(serviceName)

	livenessProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      10,
		PeriodSeconds:       15,
		InitialDelaySeconds: 5,
	}
	startupProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      10,
		PeriodSeconds:       15,
		InitialDelaySeconds: 5,
	}
	livenessProbe.Exec = &corev1.ExecAction{
		Command: []string{
			"/usr/bin/pgrep", "-r", "DRST", "-f", "designate.central",
		},
	}
	startupProbe.Exec = livenessProbe.Exec

	envVars := map[string]env.Setter{}
	envVars["KOLLA_CONFIG_STRATEGY"] = env.SetValue("COPY_ALWAYS")
	envVars["CONFIG_HASH"] = env.SetValue(configHash)

	// Add the CA bundle
	if instance.Spec.TLS.CaBundleSecretName != "" {
		volumes = append(volumes, instance.Spec.TLS.CreateVolume())
		volumeMounts = append(volumeMounts, instance.Spec.TLS.CreateVolumeMounts(nil)...)
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Replicas: instance.Spec.Replicas,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
					Labels:      labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: instance.Spec.ServiceAccount,
					Volumes:            volumes,
					Containers: []corev1.Container{
						{
							Name:  serviceName,
							Image: instance.Spec.ContainerImage,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: &rootUser,
							},
							Env:           env.MergeEnvs([]corev1.EnvVar{}, envVars),
							VolumeMounts:  volumeMounts,
							Resources:     instance.Spec.Resources,
							StartupProbe:  startupProbe,
							LivenessProbe: livenessProbe,
						},
					},
				},
			},
		},
	}

	// If possible two pods of the same service should not
	// run on the same worker node. If this is not possible
	// the get still created on the same worker node.
	deployment.Spec.Template.Spec.Affinity = affinity.DistributePods(
		common.AppSelector,
		[]string{
			serviceName,
		},
		corev1.LabelHostname,
	)
	if instance.Spec.NodeSelector != nil {
		deployment.Spec.Template.Spec.NodeSelector = *instance.Spec.NodeSelector
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
	deployment.Spec.Template.Spec.InitContainers = designate.InitContainer(initContainerDetails)

	return deployment
}
