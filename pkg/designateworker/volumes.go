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

package designateworker

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/openstack-k8s-operators/designate-operator/pkg/designate"
)

const (
	rndcSecretVolume = "rndc-secret"
)

func GetVolumes(name string) []corev1.Volume {
	var config0640AccessMode int32 = 0640
	return append(
		designate.GetVolumes(name),
		corev1.Volume{
			Name: rndcSecretVolume,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  designate.DesignateBindKeySecret,
					DefaultMode: &config0640AccessMode,
				},
			},
		},
	)
}

func GetVolumeMounts(serviceName string) []corev1.VolumeMount {
	return append(
		designate.GetVolumeMounts(serviceName),
		corev1.VolumeMount{
			Name:      rndcSecretVolume,
			MountPath: "/etc/designate/rndc-keys",
			ReadOnly:  true,
		},
	)
}
