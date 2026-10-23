/*
Copyright 2026 Red Hat

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

package v1beta1

import (
	"os"
	"testing"
)

func TestSharedServiceImageDefaults(t *testing.T) {
	originalDefaults := designateDefaults
	t.Cleanup(func() { SetupDesignateDefaults(originalDefaults) })

	const pinnedCentral = "quay.io/example/designate-central@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for _, tc := range []struct {
		name     string
		mdns     string
		producer string
	}{
		{name: "inherit pinned central image"},
		{name: "preserve MDNS override", mdns: "quay.io/example/designate-mdns:custom"},
		{name: "preserve producer override", producer: "quay.io/example/designate-producer:custom"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("RELATED_IMAGE_DESIGNATE_CENTRAL_IMAGE_URL_DEFAULT", pinnedCentral)
			for key, value := range map[string]string{
				"RELATED_IMAGE_DESIGNATE_MDNS_IMAGE_URL_DEFAULT":     tc.mdns,
				"RELATED_IMAGE_DESIGNATE_PRODUCER_IMAGE_URL_DEFAULT": tc.producer,
			} {
				t.Setenv(key, value)
				if value == "" {
					if err := os.Unsetenv(key); err != nil {
						t.Fatal(err)
					}
				}
			}
			SetupDefaults()

			spec := DesignateSpec{}
			spec.Default()
			wantMDNS, wantProducer := pinnedCentral, pinnedCentral
			if tc.mdns != "" {
				wantMDNS = tc.mdns
			}
			if tc.producer != "" {
				wantProducer = tc.producer
			}
			for service, images := range map[string][2]string{
				"central":  {spec.DesignateCentral.ContainerImage, pinnedCentral},
				"mdns":     {spec.DesignateMdns.ContainerImage, wantMDNS},
				"producer": {spec.DesignateProducer.ContainerImage, wantProducer},
			} {
				if images[0] != images[1] {
					t.Errorf("%s image = %q, want %q", service, images[0], images[1])
				}
			}
		})
	}
}
