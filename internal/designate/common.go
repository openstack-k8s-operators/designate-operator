/*
Copyright 2022.

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
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
)

// Common static errors for all designate functionality
var (
	// Controller errors
	ErrRedisRequired               = errors.New("unable to configure designate deployment without Redis")
	ErrNetworkAttachmentConfig     = errors.New("not all pods have interfaces with ips as configured in NetworkAttachments")
	ErrNetworkAttachmentNotFound   = errors.New("unable to locate network attachment")
	ErrControlNetworkNotConfigured = errors.New("designate control network attachment not configured, check NetworkAttachments and ControlNetworkName")
	// Package errors
	ErrPredictableIPAllocation     = errors.New("predictable IPs: cannot allocate IP addresses")
	ErrPredictableIPOutOfAddresses = errors.New("predictable IPs: out of available addresses")
	ErrCannotAllocateIPAddresses   = errors.New("cannot allocate IP addresses")
)

const (
	// KollaServiceCommand - the command to start the service binary in the kolla container
	KollaServiceCommand = "/usr/local/bin/kolla_set_configs && /usr/local/bin/kolla_start"
	// DesignateDatabaseName - the name of the DB to store tha API schema
	DesignateDatabaseName = "designate"
)

// GetScriptConfigMapName returns the name of the ConfigMap used for the
// config merger and the service init scripts
func GetScriptConfigMapName(crName string) string {
	return fmt.Sprintf("%s-scripts", crName)
}

// GetServiceConfigConfigMapName returns the name of the ConfigMap used to
// store the service configuration files
func GetServiceConfigConfigMapName(crName string) string {
	return fmt.Sprintf("%s-config-data", crName)
}

// DatabaseStatus -
type DatabaseStatus int

const (
	// DBFailed -
	DBFailed DatabaseStatus = iota
	// DBCreating -
	DBCreating DatabaseStatus = iota
	// DBCompleted -
	DBCompleted DatabaseStatus = iota
)

// MessageBusStatus -
type MessageBusStatus int

const (
	// MQFailed -
	MQFailed MessageBusStatus = iota
	// MQCreating -
	MQCreating MessageBusStatus = iota
	// MQCompleted -
	MQCompleted MessageBusStatus = iota
)

// Database -
type Database struct {
	Database *mariadbv1.Database
	Status   DatabaseStatus
}

// MessageBus -
type MessageBus struct {
	SecretName string
	Status     MessageBusStatus
}

// PodLabelingConfig holds configuration for pod labeling
type PodLabelingConfig struct {
	ConfigMapName string
	IPKeyPrefix   string
}

// HandlePodLabeling handles adding predictableip labels to pods
func HandlePodLabeling(ctx context.Context, h *helper.Helper, instanceName, namespace string, config PodLabelingConfig) error {
	// List all pods owned by this instance
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels{
			common.AppSelector: instanceName,
		},
	}

	err := h.GetClient().List(ctx, podList, listOpts...)
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	// Get the IP configmap once for all pods
	configMap := &corev1.ConfigMap{}
	err = h.GetClient().Get(ctx, types.NamespacedName{Name: config.ConfigMapName, Namespace: namespace}, configMap)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			return nil // Configmap not found, skip labeling
		}
		return err
	}

	// Process each pod
	for _, pod := range podList.Items {
		// Extract pod index from pod name (e.g., "designate-backendbind9-0" -> "0")
		podName := pod.Name
		nameParts := strings.Split(podName, "-")
		if len(nameParts) == 0 {
			continue // Skip invalid pod name format
		}
		podIndex := nameParts[len(nameParts)-1]

		// Get the IP for this pod index
		ipKey := fmt.Sprintf("%s%s", config.IPKeyPrefix, podIndex)
		predictableIP, exists := configMap.Data[ipKey]
		if !exists {
			continue // No IP found for this pod index, skip labeling
		}

		// Check if the label needs to be added or updated
		currentIP, hasLabel := pod.Labels[networkv1.PredictableIPLabel]
		if hasLabel && currentIP == predictableIP {
			continue // Label already exists with correct value
		}

		// Add or update the predictableip label to the pod
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[networkv1.PredictableIPLabel] = predictableIP

		// Update the pod
		err = h.GetClient().Update(ctx, &pod)
		if err != nil {
			// Log error but continue processing other pods
			continue
		}
	}

	return nil
}
