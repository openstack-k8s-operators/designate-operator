/*
Copyright 2025.
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

package controllers

import (
	topologyv1 "github.com/openstack-k8s-operators/infra-operator/apis/topology/v1beta1"
)

type topologyGetter interface {
	GetLastAppliedTopology() *topologyv1.TopoRef
}

// GetLastTopologyRef - Returns a TopoRef object that can be passed to the
// Handle topology logic
func GetLastAppliedTopologyRef(t topologyGetter, ns string) *topologyv1.TopoRef {
	lastAppliedTopologyName := ""
	if l := t.GetLastAppliedTopology(); l != nil {
		lastAppliedTopologyName = l.Name
	}
	return &topologyv1.TopoRef{
		Name:      lastAppliedTopologyName,
		Namespace: ns,
	}
}
