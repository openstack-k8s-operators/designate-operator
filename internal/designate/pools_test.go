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

package designate

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/go-logr/logr"
	designatev1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"errors"
	"testing"
)

func TestParsePoolListOutput(t *testing.T) {
	tests := []struct {
		name    string
		output  string
		want    []PoolInfo
		wantErr error
	}{
		{
			name: "two pools",
			output: "Updating Pools Configuration\n" +
				"****************************\n" +
				"The following changes will occur:\n" +
				"*********************************\n" +
				"Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>\n" +
				"Update Pool: <Pool id:'0502b5a3-11d4-4d4a-9d53-6f576448d3e3' name:'pool1'>",
			want: []PoolInfo{
				{ID: "794ccc2c-d751-44fe-b57f-8894c9f5c842", Name: "default"},
				{ID: "0502b5a3-11d4-4d4a-9d53-6f576448d3e3", Name: "pool1"},
			},
			wantErr: nil,
		},
		{
			name:   "single pool",
			output: "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>",
			want: []PoolInfo{
				{ID: "794ccc2c-d751-44fe-b57f-8894c9f5c842", Name: "default"},
			},
			wantErr: nil,
		},
		{
			name:    "unregistered pool in dry run",
			output:  "Create Pool: <Pool id:'None' name:'pool2'>",
			wantErr: ErrNoPoolsFound,
		},
		{
			name:   "registered and pending pools in dry run",
			output: "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>\nCreate Pool: <Pool id:'None' name:'pool2'>",
			want:   []PoolInfo{{ID: "794ccc2c-d751-44fe-b57f-8894c9f5c842", Name: "default"}},
		},
		{
			name:    "no pools in output",
			output:  "Some other output\nwithout pool information",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "empty output",
			output:  "",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "malformed pool line - missing id",
			output:  "Update Pool: <Pool name:'default'>",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
		{
			name:    "malformed pool line - missing name",
			output:  "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842'>",
			want:    nil,
			wantErr: ErrNoPoolsFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParsePoolListOutput(tt.output)

			// Check error
			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("ParsePoolListOutput() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if !errors.Is(err, tt.wantErr) {
					t.Errorf("ParsePoolListOutput() error = %v, wantErr %v", err, tt.wantErr)
				}
				return
			}

			if err != nil {
				t.Errorf("ParsePoolListOutput() unexpected error = %v", err)
				return
			}

			// Check length
			if len(got) != len(tt.want) {
				t.Errorf("ParsePoolListOutput() got %d pools, want %d pools", len(got), len(tt.want))
				return
			}

			// Check each pool
			for i := range got {
				if got[i].ID != tt.want[i].ID {
					t.Errorf("ParsePoolListOutput() pool[%d].ID = %v, want %v", i, got[i].ID, tt.want[i].ID)
				}
				if got[i].Name != tt.want[i].Name {
					t.Errorf("ParsePoolListOutput() pool[%d].Name = %v, want %v", i, got[i].Name, tt.want[i].Name)
				}
			}
		})
	}
}

func TestGetPoolNameToIDMap(t *testing.T) {
	tests := []struct {
		name  string
		pools []PoolInfo
		want  map[string]string
	}{
		{
			name: "two pools",
			pools: []PoolInfo{
				{ID: "id1", Name: "pool1"},
				{ID: "id2", Name: "pool2"},
			},
			want: map[string]string{
				"pool1": "id1",
				"pool2": "id2",
			},
		},
		{
			name: "single pool",
			pools: []PoolInfo{
				{ID: "default-id", Name: "default"},
			},
			want: map[string]string{
				"default": "default-id",
			},
		},
		{
			name:  "no pools",
			pools: []PoolInfo{},
			want:  map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a mock map based on the pool list
			got := make(map[string]string)
			for _, pool := range tt.pools {
				got[pool.Name] = pool.ID
			}

			// Check length
			if len(got) != len(tt.want) {
				t.Errorf("GetPoolNameToIDMap() got %d entries, want %d", len(got), len(tt.want))
				return
			}

			// Check each entry
			for name, wantID := range tt.want {
				gotID, exists := got[name]
				if !exists {
					t.Errorf("GetPoolNameToIDMap() missing pool name %s", name)
					continue
				}
				if gotID != wantID {
					t.Errorf("GetPoolNameToIDMap() pool[%s] = %s, want %s", name, gotID, wantID)
				}
			}
		})
	}
}

// poolLogTransport serves Kubernetes pod logs without starting a network server.
type poolLogTransport struct {
	logs  map[string]string
	paths []string
}

func (p *poolLogTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	p.paths = append(p.paths, req.URL.Path)
	body, ok := p.logs[req.URL.Path]
	status := http.StatusOK
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"text/plain"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

func TestPoolDiscoveryRefreshesMissingPool(t *testing.T) {
	for _, initialOutput := range []string{
		"Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>\nUpdate Pool: <Pool id:'0502b5a3-11d4-4d4a-9d53-6f576448d3e3' name:'pool1'>",
		"No registered pools yet",
		"Create Pool: <Pool id:'None' name:'pool2'>",
	} {
		t.Run(initialOutput, func(t *testing.T) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{corev1.AddToScheme, batchv1.AddToScheme, designatev1.AddToScheme} {
				if err := add(scheme); err != nil {
					t.Fatal(err)
				}
			}
			instance := &designatev1.Designate{ObjectMeta: metav1.ObjectMeta{Name: "designate", Namespace: "test", UID: "designate-uid"}}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&batchv1.Job{}).WithObjects(instance).Build()
			transport := &poolLogTransport{logs: map[string]string{
				"/api/v1/namespaces/test/pods/a-old-discovery/log": initialOutput,
				"/api/v1/namespaces/test/pods/z-new-discovery/log": "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>\nUpdate Pool: <Pool id:'0502b5a3-11d4-4d4a-9d53-6f576448d3e3' name:'pool1'>\nUpdate Pool: <Pool id:'22222222-2222-2222-2222-222222222222' name:'pool2'>",
			}}
			kube, err := kubernetes.NewForConfig(&rest.Config{Host: "https://kubernetes.test", Transport: transport})
			if err != nil {
				t.Fatal(err)
			}
			h, err := helper.NewHelper(instance, c, kube, scheme, logr.Discard())
			if err != nil {
				t.Fatal(err)
			}
			jobName := types.NamespacedName{Name: PoolListJob(instance, nil, nil).Name, Namespace: instance.Namespace}
			complete := func(uid types.UID, podName string) {
				t.Helper()
				discovery := &batchv1.Job{}
				if err := c.Get(ctx, jobName, discovery); err != nil {
					t.Fatal(err)
				}
				discovery.UID = uid
				if err := c.Update(ctx, discovery); err != nil {
					t.Fatal(err)
				}
				discovery.Status.Succeeded = 1
				if err := c.Status().Update(ctx, discovery); err != nil {
					t.Fatal(err)
				}
				pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: instance.Namespace, Labels: map[string]string{"job-name": jobName.Name, batchv1.ControllerUidLabel: string(uid)}}}
				if err := c.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}
			}
			// The first lookup starts the discovery Job for the original configuration.
			if _, err := GetPoolNameToIDMap(ctx, h, instance.Namespace, instance, "pool1"); !errors.Is(err, ErrPoolListJobNotComplete) {
				t.Fatalf("expected initial discovery to requeue, got %v", err)
			}
			complete("old-job-uid", "a-old-discovery")
			// After adding pool2, a completed snapshot of the old pools must be discarded.
			ids, err := GetPoolNameToIDMap(ctx, h, instance.Namespace, instance, "pool1", "pool2")
			if !errors.Is(err, ErrPoolListJobNotComplete) || ids != nil {
				t.Fatalf("stale discovery was accepted: %v, %v", ids, err)
			}
			if err := c.Get(ctx, jobName, &batchv1.Job{}); !k8s_errors.IsNotFound(err) {
				t.Fatalf("stale completed Job was not deleted: %v", err)
			}
			if _, err := GetPoolNameToIDMap(ctx, h, instance.Namespace, instance, "pool1", "pool2"); !errors.Is(err, ErrPoolListJobNotComplete) {
				t.Fatalf("expected refreshed discovery to start, got %v", err)
			}
			complete("new-job-uid", "z-new-discovery")
			// Keep the old Pod to model asynchronous garbage collection. Its logs must
			// never be confused with the recreated Job's result.
			ids, err = GetPoolNameToIDMap(ctx, h, instance.Namespace, instance, "pool1", "pool2")
			if err != nil {
				t.Fatal(err)
			}
			if ids["pool1"] != "0502b5a3-11d4-4d4a-9d53-6f576448d3e3" || ids["pool2"] != "22222222-2222-2222-2222-222222222222" {
				t.Fatalf("refreshed pool identities missing: %v", ids)
			}
			if len(transport.paths) != 2 || transport.paths[1] != "/api/v1/namespaces/test/pods/z-new-discovery/log" {
				t.Fatalf("incorrect discovery logs read: %v", transport.paths)
			}
		})
	}
}
