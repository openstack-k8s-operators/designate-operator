/*
Copyright 2024.

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
	designate "github.com/openstack-k8s-operators/designate-operator/internal/designate"
	corev1 "k8s.io/api/core/v1"
)

const (
	scriptVolume       = "designatebackendbind9-scripts"
	configVolume       = "designatebackendbind9-config-data"
	namedConfigVolume  = "designatebackendbind9-config-named"
	mergedConfigVolume = "designatebackendbind9-config-data-merged"
	logVolume          = "designatebackendbind9-log-volume"
	rndcKeys           = "designatebackendbind9-keys"
	bindIPs            = "designate-bind-ips"
	tsigKeys           = "designatebackendbind9-tsig"
)

// NOTE(beagles): I vacillated on using designate.GetVolumes() here and appending the extra entries and may still. There
// is a lot going on here that is different than the components configuration so I found it a little easier to lay it
// out in its own space, and look to refactor where necessary.

func getServicePodVolumes(baseConfigMapName string, bindIPConfigMapName string, tsigSecretName string) []corev1.Volume {
	var scriptMode int32 = 0755
	var configMode int32 = 0640
	volumes := []corev1.Volume{
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
			Name: namedConfigVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &configMode,
					SecretName:  baseConfigMapName + "-config-named",
				},
			},
		},
		{
			Name: mergedConfigVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
		{
			Name: logVolume,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
			},
		},
		{
			Name: rndcKeys,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &configMode,
					SecretName:  designate.DesignateBindKeySecret,
				},
			},
		},
		{
			Name: bindIPs,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					DefaultMode: &configMode,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: bindIPConfigMapName,
					},
				},
			},
		},
	}

	// Add TSIG volume if tsigSecretName is provided
	if tsigSecretName != "" {
		volumes = append(volumes, corev1.Volume{
			Name: tsigKeys,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &configMode,
					SecretName:  tsigSecretName,
					Optional:    func(b bool) *bool { return &b }(true),
				},
			},
		})
	}

	return volumes
}

// TODO(beagles): we follow the old TripleO/kolla naming of these mounts, but do they really make sense here?

// getInitVolumesMounts - the init container will use the scripts mounted in the scriptVolume and create completed named
// configuration from the files in configVolume. The modified files will be stored in the mergedConfigVolume
func getInitVolumeMounts(includeTSIG bool) []corev1.VolumeMount {
	mounts := []corev1.VolumeMount{
		{
			Name:      configVolume,
			MountPath: "/var/lib/config-data/default",
			ReadOnly:  true,
		},
		{
			Name:      namedConfigVolume,
			MountPath: "/var/lib/config-data/default/named",
			ReadOnly:  false,
		},
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  false,
		},
		{
			Name:      scriptVolume,
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      rndcKeys,
			MountPath: "/var/lib/config-data/keys",
			ReadOnly:  true,
		},
		{
			Name:      bindIPs,
			MountPath: "/var/lib/predictableips",
			ReadOnly:  true,
		},
	}

	if includeTSIG {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      tsigKeys,
			MountPath: "/var/lib/tsig/tsigkeys.conf",
			SubPath:   "tsigkeys.conf",
			ReadOnly:  true,
		})
	}

	return mounts
}

func getPredIPVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      scriptVolume,
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      bindIPs,
			MountPath: "/var/lib/predictableips",
		},
	}
}

func getServicePodVolumeMounts(persistentData string) []corev1.VolumeMount {
	// Note: TSIG secret volume is defined in getServicePodVolumes(), but only the init container
	// mounts the actual file via getInitVolumeMounts(). Init container copies it to merged config.
	return []corev1.VolumeMount{
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/config-data/merged",
			ReadOnly:  true,
		},
		{
			Name:      mergedConfigVolume,
			MountPath: "/var/lib/kolla/config_files/config.json",
			SubPath:   "designate-bind9-config.json",
			ReadOnly:  true,
		},
		{
			Name:      scriptVolume,
			MountPath: "/usr/local/bin/container-scripts",
			ReadOnly:  true,
		},
		{
			Name:      persistentData,
			MountPath: "/var/named-persistent",
			ReadOnly:  false,
		},
		{
			Name:      logVolume,
			MountPath: "/var/log/bind",
			ReadOnly:  false,
		},
	}
}
