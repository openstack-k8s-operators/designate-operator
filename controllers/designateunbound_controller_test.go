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

package controllers

import (
	"context"
	"reflect"
	"testing"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	operatorv1 "github.com/openshift/api/operator/v1"
	"github.com/openstack-k8s-operators/designate-operator/pkg/designateunbound"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func Test_getCIDRsFromNADs(t *testing.T) {
	type args struct {
		nadList []networkv1.NetworkAttachmentDefinition
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		// TODO: Add test cases.
		{
			name: "multiple-nads",
			args: args{
				nadList: []networkv1.NetworkAttachmentDefinition{
					{
						Spec: networkv1.NetworkAttachmentDefinitionSpec{
							Config: `{"cniVersion": "0.3.1", "name": "designate-nad", "type": "macvlan", "master": "eth0.26", "ipam": {"type": "whereabouts", "range": "172.10.10.0/24", "range_start": "172.10.10.10", "range_end": "172.10.10.20"}}`,
						},
					},
					{
						Spec: networkv1.NetworkAttachmentDefinitionSpec{
							Config: `{"cniVersion": "0.3.1", "name": "other-nad", "type": "macvlan", "master": "eth0.27", "ipam": {"type": "whereabouts", "range": "172.30.10.0/24", "range_start": "172.30.10.10", "range_end": "172.30.10.20"}}`,
						},
					},
				},
			},
			want:    []string{"172.10.10.0/24", "172.30.10.0/24"},
			wantErr: false,
		},
		{
			name: "single-nad",
			args: args{
				nadList: []networkv1.NetworkAttachmentDefinition{
					{
						Spec: networkv1.NetworkAttachmentDefinitionSpec{
							Config: `{"cniVersion": "0.3.1", "name": "designate-nad", "type": "macvlan", "master": "eth0.26", "ipam": {"type": "whereabouts", "range": "172.20.10.0/24", "range_start": "172.20.10.10", "range_end": "172.20.10.20"}}`,
						},
					},
				},
			},
			want:    []string{"172.20.10.0/24"},
			wantErr: false,
		},
		{
			name: "no-nads",
			args: args{
				nadList: []networkv1.NetworkAttachmentDefinition{},
			},
			want:    []string{},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getCIDRsFromNADs(tt.args.nadList)
			if (err != nil) != tt.wantErr {
				t.Errorf("getCIDRsFromNADs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getCIDRsFromNADs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUnboundReconciler_getClusterJoinSubnets(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = operatorv1.AddToScheme(scheme)

	tests := []struct {
		name    string
		network *operatorv1.Network
		want    []string
		wantErr bool
	}{
		{
			name: "custom-join-subnets-both-ipv4-and-ipv6",
			network: &operatorv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.NetworkSpec{
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							IPv4: &operatorv1.IPv4OVNKubernetesConfig{
								InternalJoinSubnet: "192.168.1.0/24",
							},
							IPv6: &operatorv1.IPv6OVNKubernetesConfig{
								InternalJoinSubnet: "2001:db8::/64",
							},
						},
					},
				},
			},
			want:    []string{"192.168.1.0/24", "2001:db8::/64"},
			wantErr: false,
		},
		{
			name: "custom-join-subnet-ipv4-only",
			network: &operatorv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.NetworkSpec{
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							IPv4: &operatorv1.IPv4OVNKubernetesConfig{
								InternalJoinSubnet: "10.0.0.0/16",
							},
						},
					},
				},
			},
			want:    []string{"10.0.0.0/16", designateunbound.DefaultJoinSubnetV6},
			wantErr: false,
		},
		{
			name: "custom-join-subnet-ipv6-only",
			network: &operatorv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.NetworkSpec{
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							IPv6: &operatorv1.IPv6OVNKubernetesConfig{
								InternalJoinSubnet: "fd12:3456::/64",
							},
						},
					},
				},
			},
			want:    []string{designateunbound.DefaultJoinSubnetV4, "fd12:3456::/64"},
			wantErr: false,
		},
		{
			name: "no-ovn-config-uses-defaults",
			network: &operatorv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.NetworkSpec{
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						// No OVNKubernetesConfig
					},
				},
			},
			want:    []string{designateunbound.DefaultJoinSubnetV4, designateunbound.DefaultJoinSubnetV6},
			wantErr: false,
		},
		{
			name: "empty-join-subnets-uses-defaults",
			network: &operatorv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1.NetworkSpec{
					DefaultNetwork: operatorv1.DefaultNetworkDefinition{
						OVNKubernetesConfig: &operatorv1.OVNKubernetesConfig{
							IPv4: &operatorv1.IPv4OVNKubernetesConfig{
								InternalJoinSubnet: "",
							},
							IPv6: &operatorv1.IPv6OVNKubernetesConfig{
								InternalJoinSubnet: "",
							},
						},
					},
				},
			},
			want:    []string{designateunbound.DefaultJoinSubnetV4, designateunbound.DefaultJoinSubnetV6},
			wantErr: false,
		},
		{
			name:    "network-not-found-uses-defaults",
			network: nil, // Network object doesn't exist
			want:    []string{designateunbound.DefaultJoinSubnetV4, designateunbound.DefaultJoinSubnetV6},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var objs []client.Object
			if tt.network != nil {
				objs = append(objs, tt.network)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(objs...).
				Build()

			r := &UnboundReconciler{
				Client: fakeClient,
			}

			// getClusterJoinSubnets never returns an error.
			got := r.getClusterJoinSubnets(context.TODO())
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("UnboundReconciler.getClusterJoinSubnets() = %v, want %v", got, tt.want)
			}
		})
	}
}
