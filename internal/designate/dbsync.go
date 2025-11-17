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
	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/env"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// DBSyncCommand -
	DBSyncCommand = "/usr/local/bin/kolla_set_configs && /usr/local/bin/kolla_start"
)

// DbSyncJob func
func DbSyncJob(
	instance *designatev1beta1.Designate,
	labels map[string]string,
	annotations map[string]string,
) *batchv1.Job {
	runAsUser := int64(0)

	volumeDefs := []VolumeMapping{
		{Name: ScriptsVolumeName(ServiceName), Type: ScriptMount, MountPath: "/usr/local/bin/container-scripts"},
		{Name: ConfigVolumeName(ServiceName), Type: SecretMount, MountPath: "/var/lib/config-data/default"},
		{Name: "db-sync-config-data-merged", Type: MergeMount, MountPath: "/var/lib/config-data/merged"},
	}

	volumes, initVolumeMounts := ProcessVolumes(volumeDefs)

	volumeMounts := append(initVolumeMounts, corev1.VolumeMount{
		Name:      "db-sync-config-data-merged",
		MountPath: "/var/lib/kolla/config_files/config.json",
		SubPath:   "db-sync-config.json",
		ReadOnly:  true,
	})

	args := []string{"-c", DBSyncCommand}

	envVars := map[string]env.Setter{}
	envVars["KOLLA_CONFIG_STRATEGY"] = env.SetValue("COPY_ALWAYS")

	// add CA cert if defined
	if instance.Spec.DesignateAPI.TLS.CaBundleSecretName != "" {
		volumes = append(volumes, instance.Spec.DesignateAPI.TLS.CreateVolume())
		volumeMounts = append(volumeMounts, instance.Spec.DesignateAPI.TLS.CreateVolumeMounts(nil)...)
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      instance.Name + "-db-sync",
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyOnFailure,
					ServiceAccountName: instance.RbacResourceName(),
					Containers: []corev1.Container{
						{
							Name: instance.Name + "-db-sync",
							Command: []string{
								"/bin/bash",
							},
							Args:  args,
							Image: instance.Spec.DesignateAPI.ContainerImage,
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: &runAsUser,
							},
							Env:          env.MergeEnvs([]corev1.EnvVar{}, envVars),
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	initContainerDetails := APIDetails{
		ContainerImage:       instance.Spec.DesignateAPI.ContainerImage,
		DatabaseHost:         instance.Status.DatabaseHostname,
		DatabaseName:         DatabaseName,
		OSPSecret:            instance.Spec.Secret,
		UserPasswordSelector: instance.Spec.PasswordSelectors.Service,
		VolumeMounts:         initVolumeMounts,
	}
	job.Spec.Template.Spec.InitContainers = InitContainer(initContainerDetails)

	if instance.Spec.NodeSelector != nil {
		job.Spec.Template.Spec.NodeSelector = *instance.Spec.NodeSelector
	}

	return job
}
