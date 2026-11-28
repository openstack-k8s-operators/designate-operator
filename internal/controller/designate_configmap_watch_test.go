/*
Copyright 2026.

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

package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/openstack-k8s-operators/designate-operator/internal/designate"
)

func TestFindDesignatesForConfigMap(t *testing.T) {
	r, _, _ := multipoolMigrationTestHelper(t)

	tests := []struct {
		name      string
		configMap string
		want      int
	}{
		{name: "multipool ConfigMap", configMap: designate.MultipoolConfigMapName, want: 1},
		{name: "NS records ConfigMap", configMap: designate.NsRecordsConfigMap, want: 1},
		{name: "unrelated ConfigMap", configMap: "unrelated", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: tt.configMap, Namespace: "test"},
			}
			requests := r.findDesignatesForConfigMap(context.Background(), obj)
			if len(requests) != tt.want {
				t.Fatalf("expected %d requests, got %d: %+v", tt.want, len(requests), requests)
			}
			if tt.want == 1 && requests[0].Name != "designate" {
				t.Fatalf("expected request for 'designate', got %+v", requests[0])
			}
		})
	}
}
