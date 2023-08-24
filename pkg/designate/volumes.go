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
	var scriptMode int32 = 0740
	var configMode int32 = 0640

	return []corev1.Volume{
		{
			Name: scriptVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &scriptMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: baseConfigMapName + "-scripts",
					},
				},
			},
		},
		{
			Name: configVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &configMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: baseConfigMapName + "-config-data",
					},
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
			Name:      "scripts",
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      "config-data",
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
		},
		{
			Name:      "config-data-merged",
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
	}
}

// GetServiceVolumeMounts - VolumeMounts to get access to the merged
// configuration
func GetServiceVolumeMounts() []corev1.VolumeMount {
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
	}
}

// GetOpenstackVolumeMounts - VolumeMounts use to inject config and scripts
func GetOpenstackVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      configVolume,
			MountPath: "/var/lib/openstack/config",
			ReadOnly:  false,
		},
		{
			Name:      logVolume,
			MountPath: "/var/log/ndesignate",
			ReadOnly:  false,
		},
	}
}

// GetOpenstackVolumes - returns the volumes used for the service deployment and for
// any jobs needs access for the full service configuration
func GetOpenstackVolumes(serviceConfigConfigMapName string) []corev1.Volume {
	var configMode int32 = 0640
	return []corev1.Volume{
		{
			Name: configVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &configMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: serviceConfigConfigMapName,
					},
				},
			},
		},
		{
			Name: logVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
	}
}

// #############################################################################
// getVolumes
func getVolumes(name string) []corev1.Volume {
	var scriptsVolumeDefaultMode int32 = 0755
	var config0640AccessMode int32 = 0640

	return []corev1.Volume{
		{
			Name: "scripts",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &scriptsVolumeDefaultMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: name + "-scripts",
					},
				},
			},
		},
		{
			Name: "config-data",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &config0640AccessMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: name + "-config-data",
					},
				},
			},
		},
		{
			Name: "config-data-merged",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
	}
}

// getInitVolumeMounts - general init task VolumeMounts
func getInitVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "scripts",
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      "config-data",
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
		},
		{
			Name:      "config-data-merged",
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
	}
}

// getVolumeMounts - general VolumeMounts
func getVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "scripts",
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      "config-data-merged",
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
	}
}
