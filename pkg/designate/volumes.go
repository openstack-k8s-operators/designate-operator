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
	corev1 "k8s.io/api/core/v1"
)

const (
	scriptVolume       = "scripts"
	configVolume       = "config-data"
	mergedConfigVolume = "config-data-merged"
	logVolume          = "logs"
)

// GetVolumes - returns the volumes used for the service deployment and for
// any jobs needs access for the full service configuration
func GetVolumes(baseConfigMapName string) []corev1.Volume {
	var scriptMode int32 = 0755
	var configMode int32 = 0640

	return []corev1.Volume{
		{
			Name: scriptVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &scriptMode,
					SecretName:  baseConfigMapName + "-scripts",
				},
			},
		},
		{
			Name: configVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &configMode,
					SecretName:  baseConfigMapName + "-config-data",
				},
			},
		},
		{
			Name: mergedConfigVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
	}
}

// GetInitVolumeMounts - Designate Control Plane init task VolumeMounts
func GetInitVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      scriptVolume,
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      configVolume,
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
		},
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
	}
}

// GetVolumeMounts - VolumeMounts to get access to the merged
// configuration
func GetVolumeMounts(serviceName string) []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      scriptVolume,
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/kolla/config_files/config.json",
			SubPath:   serviceName + "-config.json",
			ReadOnly:  true,
		},
	}
}
