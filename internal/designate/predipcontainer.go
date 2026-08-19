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
	"k8s.io/utils/ptr"
)

// PredIPContainerDetails contains configuration for predictable IP containers
type PredIPContainerDetails struct {
	ContainerImage string
	VolumeMounts   []corev1.VolumeMount
	Command        string
	EnvVars        []corev1.EnvVar
}

// PredictableIPContainer creates a container with predictable IP configuration
func PredictableIPContainer(init PredIPContainerDetails) corev1.Container {

	args := []string{
		"-c",
		init.Command,
	}

	// Setting the predictable IP alias only requires NET_ADMIN (netlink
	// address add). SYS_ADMIN/SYS_NICE were previously added here but are
	// unused by setipalias.py and unnecessarily widen the escape surface
	// of an init container whose image comes from the untrusted
	// NetUtilsImage CR field. CAP_CHOWN is needed because some callers
	// (mdns) additionally run crudini against the merged config, which
	// preserves the root:<service> group ownership of the file it rewrites.
	capabilities := []corev1.Capability{"NET_ADMIN", "CHOWN"}
	return corev1.Container{
		Name:  "predictableips",
		Image: init.ContainerImage,
		SecurityContext: &corev1.SecurityContext{
			Capabilities: &corev1.Capabilities{
				Add:  capabilities,
				Drop: []corev1.Capability{"ALL"},
			},
			RunAsUser:                ptr.To(int64(0)),
			RunAsNonRoot:             ptr.To(false),
			AllowPrivilegeEscalation: ptr.To(false),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Command: []string{
			"/bin/bash",
		},
		Args:         args,
		Env:          init.EnvVars,
		VolumeMounts: init.VolumeMounts,
	}
}
