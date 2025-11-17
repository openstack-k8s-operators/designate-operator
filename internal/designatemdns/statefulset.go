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

package designatemdns

import (
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	designate "github.com/openstack-k8s-operators/designate-operator/internal/designate"
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
	common "github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/affinity"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatefulSet func
func StatefulSet(
	instance *designatev1beta1.DesignateMdns,
	configHash string,
	labels map[string]string,
	annotations map[string]string,
	topology *topologyv1.Topology,
) *appsv1.StatefulSet {
	rootUser := int64(0)
	serviceName := instance.Name

	// Pods using predictable IPs and setipalias.py have both a different script path and an additonal predictable IP
	// mount. Instead of putting some convoluted logic in the common case, just explode out the individual mappings
	// here.
	volumeDefs := []designate.VolumeMapping{
		{Name: designate.ScriptsVolumeName(instance.Name), Type: designate.ScriptMount, MountPath: "/usr/local/bin/container-scripts"},
		{Name: designate.ConfigVolumeName(designate.GetOwningDesignateName(instance)), Type: designate.SecretMount, MountPath: "/var/lib/config-data/default"},
		{Name: designate.ConfigVolumeName(instance.Name), Type: designate.SecretMount, MountPath: "/var/lib/config-data/service"},
		{Name: designate.MergedVolumeName(instance.Name), Type: designate.MergeMount, MountPath: "/var/lib/config-data/merged"},
		{Name: designate.MdnsPredIPConfigMap, Type: designate.ConfigMount, MountPath: "/var/lib/predictableips"},
		{Name: designate.DefaultsVolumeName(designate.GetOwningDesignateName(instance)), Type: designate.SecretMount, MountPath: "/var/lib/config-data/common-overwrites"},
		{Name: designate.DefaultsVolumeName(instance.Name), Type: designate.SecretMount, MountPath: "/var/lib/config-data/overwrites"},
		{Name: designate.MergedDefaultsVolumeName(instance.Name), Type: designate.MergeMount, MountPath: "/var/lib/config-data/config-overwrites"},
	}

	volumes, initVolumeMounts := designate.ProcessVolumes(volumeDefs)

	volumeMounts := append(initVolumeMounts, corev1.VolumeMount{
		Name:      designate.MergedVolumeName(instance.Name),
		MountPath: "/var/lib/kolla/config_files/config.json",
		SubPath:   serviceName + "-config.json",
		ReadOnly:  true,
	})

	// Ideally we would use the connection probe but the mdns service does
	// not listen on a cluster allocated IP
	livenessProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      15,
		PeriodSeconds:       13,
		InitialDelaySeconds: 15,
	}
	startupProbe := &corev1.Probe{
		// TODO might need tuning
		TimeoutSeconds:      15,
		PeriodSeconds:       13,
		InitialDelaySeconds: 10,
	}

	livenessProbe.Exec = &corev1.ExecAction{
		Command: []string{
			"/usr/bin/pgrep", "-r", "DRST", "-f", "designate.mdns",
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

	statefulSet := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
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

	if instance.Spec.NodeSelector != nil {
		statefulSet.Spec.Template.Spec.NodeSelector = *instance.Spec.NodeSelector
	}

	if topology != nil {
		topology.ApplyTo(&statefulSet.Spec.Template)
	} else {
		// If possible two pods of the same service should not
		// run on the same worker node. If this is not possible
		// the get still created on the same worker node.
		statefulSet.Spec.Template.Spec.Affinity = affinity.DistributePods(
			common.AppSelector,
			[]string{
				serviceName,
			},
			corev1.LabelHostname,
		)
	}

	envVars = map[string]env.Setter{}
	envVars["POD_NAME"] = env.DownwardAPI("metadata.name")
	envVars["MAP_PREFIX"] = env.SetValue("mdns_address_")
	podEnv := env.MergeEnvs([]corev1.EnvVar{}, envVars)
	initContainerDetails := designate.InitContainerDetails{
		ContainerImage: instance.Spec.ContainerImage,
		VolumeMounts:   initVolumeMounts,
		EnvVars:        podEnv,
	}
	predIPContainerDetails := designate.PredIPContainerDetails{
		ContainerImage: instance.Spec.NetUtilsImage,
		VolumeMounts:   initVolumeMounts,
		EnvVars:        podEnv,
		Command:        designate.PredictableIPCommand,
	}

	statefulSet.Spec.Template.Spec.InitContainers = []corev1.Container{
		designate.SimpleInitContainer(initContainerDetails),
		designate.PredictableIPContainer(predIPContainerDetails),
	}

	return statefulSet
}
