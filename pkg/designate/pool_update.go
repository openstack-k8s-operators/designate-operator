package designate

import (
	"fmt"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	PoolUpdateCommand      = "/usr/bin/designate-manage pool update"
	DesignateConfigVolume  = "designate-config"
	DesignateConfigMount   = "/etc/designate"
	DesignateConfigKeyPath = "designate.conf"
	DesignatePoolsYamlPath = "pools.yaml"
)

func PoolUpdateJob(
	instance *designatev1beta1.Designate,
	labels map[string]string,
	annotations map[string]string,
) *batchv1.Job {
	runAsUser := int64(0)
	volumeMounts := GetVolumeMounts(instance.Name)
	volumes := GetVolumes(instance.Name)

	volumes = append(volumes, corev1.Volume{
		Name: "pools-yaml-config",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: PoolsYamlConfigMap,
				},
				Items: []corev1.KeyToPath{
					{
						Key:  PoolsYamlContent,
						Path: DesignatePoolsYamlPath,
					},
				},
			},
		},
	})

	volumes = append(volumes, corev1.Volume{
		Name: DesignateConfigVolume,
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "designate-central-config-data",
				Items: []corev1.KeyToPath{
					{
						Key:  "designate.conf",
						Path: DesignateConfigKeyPath,
					},
				},
			},
		},
	})

	volumes = append(volumes, corev1.Volume{
		Name: "rabbitmq-certs",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: "cert-rabbitmq-svc",
			},
		},
	})

	volumeMounts = append(volumeMounts,
		corev1.VolumeMount{
			Name:      "pools-yaml-config",
			MountPath: "/tmp/designate-pools",
			ReadOnly:  true,
		},
		corev1.VolumeMount{
			Name:      DesignateConfigVolume,
			MountPath: DesignateConfigMount,
			ReadOnly:  true,
		},
		corev1.VolumeMount{
			Name:      "rabbitmq-certs",
			MountPath: "/etc/pki/rabbitmq",
			ReadOnly:  true,
		},
	)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ServiceName + "-pool-update",
			Namespace: instance.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: annotations,
				},
				Spec: corev1.PodSpec{
					RestartPolicy:      corev1.RestartPolicyNever,
					ServiceAccountName: instance.RbacResourceName(),
					Containers: []corev1.Container{
						{
							Name:  ServiceName + "-pool-update",
							Image: instance.Spec.DesignateCentral.ContainerImage,
							Env: []corev1.EnvVar{
								{
									Name:  "SSL_CERT_FILE",
									Value: "/etc/pki/rabbitmq/ca.crt",
								},
							},
							Command: []string{
								"/bin/bash",
								"-c",
								fmt.Sprintf("%s --file /tmp/designate-pools/%s",
									PoolUpdateCommand,
									DesignatePoolsYamlPath,
								),
							},
							SecurityContext: &corev1.SecurityContext{
								RunAsUser: &runAsUser,
							},
							VolumeMounts: volumeMounts,
						},
					},
					Volumes: volumes,
				},
			},
		},
	}
	return job
}
