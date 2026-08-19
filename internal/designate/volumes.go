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
	"fmt"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// VolumeMapping represents a volume mapping configuration for containers
type VolumeMapping struct {
	Name      string
	Type      string
	MountPath string
	Source    string
}

const (
	// ScriptMount represents the script mount type
	ScriptMount = "script-mount"
	// SecretMount represents the secret mount type
	SecretMount = "secret-mount"
	// ConfigMount represents the config mount type
	ConfigMount = "config-mount"
	// MergeMount represents the merge mount type
	MergeMount = "merge-mount"
)

// GetStandardVolumeMapping returns the standard volume mappings for a designate instance
func GetStandardVolumeMapping(instance client.Object) []VolumeMapping {
	return []VolumeMapping{
		{Name: ScriptsVolumeName(GetOwningDesignateName(instance)), Type: ScriptMount, MountPath: "/usr/local/bin/container-scripts"},
		{Name: ConfigVolumeName(GetOwningDesignateName(instance)), Type: SecretMount, MountPath: "/var/lib/config-data/default"},
		{Name: DefaultsVolumeName(GetOwningDesignateName(instance)), Type: SecretMount, MountPath: "/var/lib/config-data/common-overwrites"},
		{Name: ConfigVolumeName(instance.GetName()), Type: SecretMount, MountPath: "/var/lib/config-data/service"},
		{Name: MergedVolumeName(instance.GetName()), Type: MergeMount, MountPath: "/var/lib/config-data/merged"},
		{Name: DefaultsVolumeName(instance.GetName()), Type: SecretMount, MountPath: "/var/lib/config-data/overwrites"},
		{Name: MergedDefaultsVolumeName(instance.GetName()), Type: MergeMount, MountPath: "/var/lib/config-data/config-overwrites"},
	}
}

// ProcessVolumes takes a slice of VolumeMapping and creates corresponding slices of Volumes and Mounts. This
// helps keep naming and matching of volumes and mounts in sync and consistent.
func ProcessVolumes(volumeDefs []VolumeMapping) ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := make([]corev1.Volume, len(volumeDefs))
	mounts := make([]corev1.VolumeMount, len(volumeDefs))
	modeMap := map[string]int32{
		ScriptMount: 0755,
		SecretMount: 0440,
		ConfigMount: 0440,
		MergeMount:  0,
	}
	for i := range volumeDefs {
		v := &volumeDefs[i]
		accessMode := modeMap[v.Type]
		var newVolume corev1.Volume
		var newMount corev1.VolumeMount
		switch v.Type {
		case SecretMount, ScriptMount:
			source := v.Name
			if len(v.Source) > 0 {
				source = v.Source
			}
			newVolume = corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: &accessMode,
						SecretName:  source,
					},
				},
			}
			newMount = corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
				ReadOnly:  true,
			}
		case ConfigMount:
			source := v.Name
			if len(v.Source) > 0 {
				source = v.Source
			}
			newVolume = corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: source,
						},
						DefaultMode: &accessMode,
					},
				},
			}
			newMount = corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
				ReadOnly:  true,
			}
		default:
			newVolume = corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{Medium: ""},
				},
			}
			newMount = corev1.VolumeMount{
				Name:      v.Name,
				MountPath: v.MountPath,
				ReadOnly:  false,
			}
		}
		volumes[i] = newVolume
		mounts[i] = newMount
	}
	return volumes, mounts
}

// GetConfVolumeMounts returns the final-path SubPath mounts for the config
// files the merge init container produces in the "merged" EmptyDir:
// designate.conf, designate.conf.d/custom.conf, and /etc/my.cnf.
func GetConfVolumeMounts(mergedVolumeName string) []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      mergedVolumeName,
			MountPath: "/etc/designate/designate.conf",
			SubPath:   "designate.conf",
			ReadOnly:  true,
		},
		{
			Name:      mergedVolumeName,
			MountPath: "/etc/designate/designate.conf.d/custom.conf",
			SubPath:   "custom.conf",
			ReadOnly:  true,
		},
		{
			Name:      mergedVolumeName,
			MountPath: "/etc/my.cnf",
			SubPath:   "my.cnf",
			ReadOnly:  true,
		},
	}
}

// GetHttpdConfVolumeMounts returns the final-path SubPath mounts for
// designate-api's httpd config. httpd.conf/ssl.conf ship alongside
// designate.conf in the same config-data Secret, so the merge init
// container copies them into the same "merged" EmptyDir as designate.conf.
func GetHttpdConfVolumeMounts(mergedVolumeName string) []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      mergedVolumeName,
			MountPath: "/etc/httpd/conf/httpd.conf",
			SubPath:   "httpd.conf",
			ReadOnly:  true,
		},
		{
			Name:      mergedVolumeName,
			MountPath: "/etc/httpd/conf.d/ssl.conf",
			SubPath:   "ssl.conf",
			ReadOnly:  true,
		},
	}
}

// GetConfigOverwriteVolumeMounts returns SubPath mounts that place each
// DefaultConfigOverwrite key as an individual file under basePath, sourced
// from the "config-overwrites" merged EmptyDir.
func GetConfigOverwriteVolumeMounts(mergedDefaultsVolumeName string, overwriteKeys []string, basePath string) []corev1.VolumeMount {
	mounts := make([]corev1.VolumeMount, 0, len(overwriteKeys))
	sorted := make([]string, len(overwriteKeys))
	copy(sorted, overwriteKeys)
	slices.Sort(sorted)
	for _, key := range sorted {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      mergedDefaultsVolumeName,
			MountPath: fmt.Sprintf("%s/%s", basePath, key),
			SubPath:   key,
			ReadOnly:  true,
		})
	}
	return mounts
}
