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
		SecretMount: 0640,
		ConfigMount: 0640,
		MergeMount:  0,
	}
	for i := 0; i < len(volumeDefs); i++ {
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
