/*
Copyright 2025.
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

package designateunbound

import (
	corev1 "k8s.io/api/core/v1"
)

const (
	configVolume           = "designateunbound-config"
	forwardersConfigVolume = "designateunbound-forwarders-config"
)

func GetVolumes(baseConfigMapName string) []corev1.Volume {
	var configMode int32 = 0640
	forwardersConfigOptional := true

	return []corev1.Volume{
		{
			Name: configVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  baseConfigMapName + "-config-data",
					DefaultMode: &configMode,
				},
			},
		},
		{
			Name: forwardersConfigVolume,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: baseConfigMapName + "-forwarders-config",
					},
					Optional:    &forwardersConfigOptional,
					DefaultMode: &configMode,
				},
			},
		},
	}
}

func GetVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      configVolume,
			MountPath: "/etc/unbound/conf.d",
			ReadOnly:  true,
		},
		{
			Name:      forwardersConfigVolume,
			MountPath: "/etc/unbound/conf.d/forwarders",
			ReadOnly:  true,
		},
	}
}
