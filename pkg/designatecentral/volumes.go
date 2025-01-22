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
	designate "github.com/openstack-k8s-operators/designate-operator/pkg/designate"
	corev1 "k8s.io/api/core/v1"
)

func getServicePodVolumes(serviceName string) []corev1.Volume {
	// var configMode int32 = 0640
	// return append(designate.GetVolumes(serviceName), corev1.Volume{
	// 	Name: "pools-yaml-config",
	// 	VolumeSource: corev1.VolumeSource{
	// 		ConfigMap: &corev1.ConfigMapVolumeSource{
	// 			LocalObjectReference: corev1.LocalObjectReference{
	// 				Name: designate.PoolsYamlConfigMap,
	// 			},
	// 			Items: []corev1.KeyToPath{
	// 				{
	// 					Key:  designate.PoolsYamlContent,
	// 					Path: "pools.yaml",
	// 				},
	// 			},
	// 			DefaultMode: &configMode,
	// 		},
	// 	},
	// })
	return designate.GetVolumes(serviceName)
}

func getServicePodVolumeMounts(serviceName string) []corev1.VolumeMount {
	// return append(designate.GetVolumeMounts(serviceName), corev1.VolumeMount{
	// 	Name:      "pools-yaml-config",
	// 	MountPath: "/etc/designate/pools.yaml",
	// 	SubPath:   "pools.yaml",
	// 	ReadOnly:  true,
	// })
	return designate.GetVolumeMounts(serviceName)
}
