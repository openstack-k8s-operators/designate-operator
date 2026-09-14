package designate

import (
	"fmt"
	"path/filepath"
	"time"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/pod"
	"github.com/openstack-k8s-operators/lib-common/modules/users"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

const (
	// DesignateCentralServiceName is the volume name for designate configuration
	DesignateCentralServiceName = "designate-central"
	// DesignateConfigMount is the mount path for designate configuration
	DesignateConfigMount = "/etc/designate/designate.conf.d"
	// DesignatePoolsYamlFilename is the path to the designate pools YAML file
	DesignatePoolsYamlFilename = "pools.yaml"
	// DesignatePoolTmpPath is the path where the pools YAML file will be mounted/located
	DesignatePoolTmpPath = "/tmp/designate-pools"
)

func poolFilePath() string {
	return filepath.Join(DesignatePoolTmpPath, DesignatePoolsYamlFilename)
}

func getVolumeInfo(name string) ([]corev1.Volume, []corev1.VolumeMount) {
	const projectedVolumeName = "designate-job-config-volume"
	const poolsInfoVolumeName = "pools-yaml-config"

	volumeDefs := []VolumeMapping{
		{Name: ScriptsVolumeName(name), Type: ScriptMount, MountPath: "/usr/local/bin/container-scripts"},
		{Name: "pools-yaml-merged", Type: MergeMount, MountPath: "/var/lib/config-data/merged"},
	}
	volumes, volumeMounts := ProcessVolumes(volumeDefs)

	volumeProjections := []corev1.VolumeProjection{
		{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ConfigVolumeName(DesignateCentralServiceName),
				},
			},
		},
		{
			Secret: &corev1.SecretProjection{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ConfigVolumeName(name),
				},
			},
		},
	}

	// configVolume will contain the config secrets for the desginate and designate-central resources.
	configVolume := corev1.Volume{
		Name: projectedVolumeName,
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				Sources:     volumeProjections,
				DefaultMode: ptr.To(int32(0440)),
			},
		},
	}
	volumes = append(volumes, configVolume)

	// Setup the pools yaml volume
	volumes = append(volumes, corev1.Volume{
		Name: poolsInfoVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: PoolsYamlConfigMap,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  PoolsYamlContent,
						Path: DesignatePoolsYamlFilename,
					},
				},
			},
		},
	})

	volumeMounts = append(volumeMounts,
		corev1.VolumeMount{
			Name:      poolsInfoVolumeName,
			MountPath: DesignatePoolTmpPath,
			ReadOnly:  true,
		},
		corev1.VolumeMount{
			Name:      projectedVolumeName,
			MountPath: DesignateConfigMount,
			ReadOnly:  true,
		},
	)

	return volumes, volumeMounts
}

func createJob(
	instance *designatev1beta1.Designate,
	labels map[string]string,
	annotations map[string]string,
	jobName string,
	extraArgs string,
	setDeadline bool,
) *batchv1.Job {
	volumes, volumeMounts := getVolumeInfo(instance.Name)

	envVars := []corev1.EnvVar{}
	if instance.Spec.DesignateAPI.TLS.CaBundleSecretName != "" {
		volumes = append(volumes, instance.Spec.DesignateAPI.TLS.CreateVolume())
		volumeMounts = append(volumeMounts, instance.Spec.DesignateAPI.TLS.CreateVolumeMounts(nil)...)
		envVars = append(envVars, corev1.EnvVar{
			Name:  "SSL_CERT_FILE",
			Value: "/etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem",
		})
		envVars = append(envVars, corev1.EnvVar{
			Name:  "SSL_CERT_DIR",
			Value: "/etc/pki/ca-trust/extracted/pem",
		})
	}

	cmdLine := fmt.Sprintf("/usr/bin/designate-manage --config-dir %s pool update --file %s %s",
		DesignateConfigMount,
		poolFilePath(),
		extraArgs,
	)

	jobSpec := batchv1.JobSpec{
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				RestartPolicy:                corev1.RestartPolicyNever,
				ServiceAccountName:           instance.RbacResourceName(),
				AutomountServiceAccountToken: ptr.To(false),
				SecurityContext:              pod.RestrictivePodSecurityContext(users.DesignateUID, users.DesignateGID),
				Containers: []corev1.Container{
					{
						Name:  jobName,
						Image: instance.Spec.DesignateCentral.ContainerImage,
						Env:   envVars,
						Command: []string{
							"/bin/bash",
							"-c",
							cmdLine,
						},
						SecurityContext: pod.RestrictiveSecurityContext(users.DesignateUID, users.DesignateGID),
						VolumeMounts:    volumeMounts,
					},
				},
				Volumes: volumes,
			},
		},
	}
	if setDeadline {
		// Enforce timeout at Job level to prevent hangs (e.g., during RabbitMQ issues)
		jobSpec.ActiveDeadlineSeconds = ptr.To(int64(60))
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: jobSpec,
	}
	return job
}

// PoolUpdateJob creates a job for updating designate pools
func PoolUpdateJob(
	instance *designatev1beta1.Designate,
	labels map[string]string,
	annotations map[string]string,
) *batchv1.Job {
	return createJob(
		instance,
		labels,
		annotations,
		fmt.Sprintf("%s-pool-update-%d", ServiceName, time.Now().Unix()),
		"",
		false,
	)
}

// PoolListJob creates a job for listing existing pools
func PoolListJob(
	instance *designatev1beta1.Designate,
	labels map[string]string,
	annotations map[string]string,
) *batchv1.Job {
	return createJob(
		instance,
		labels,
		annotations,
		fmt.Sprintf("%s-pool-list", ServiceName),
		"--dry-run",
		true,
	)
}
