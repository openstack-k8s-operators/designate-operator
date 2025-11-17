/*
Copyright 2023.

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

// Package designateunbound contains designate unbound constants and configuration.
package designateunbound

const (
	// Component represents the designate unbound component name
	Component = "designate-unbound"
	// ServiceName is the name of the designate unbound service
	ServiceName = "designateunbound"
	// DefaultJoinSubnetV4 is the default join subnet for IPv4
	DefaultJoinSubnetV4 = "100.64.0.0/16"
	// DefaultJoinSubnetV6 is the default join subnet for IPv6
	DefaultJoinSubnetV6 = "fd98::/64"
)
