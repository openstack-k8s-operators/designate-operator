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
	"errors"
	"fmt"

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
