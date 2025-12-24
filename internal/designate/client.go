/*
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
	"net/url"
	"time"

	"github.com/gophercloud/gophercloud/v2"
	gophercloudopenstack "github.com/gophercloud/gophercloud/v2/openstack"
	keystonev1 "github.com/openstack-k8s-operators/keystone-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/endpoint"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	"github.com/openstack-k8s-operators/lib-common/modules/openstack"
	ctrl "sigs.k8s.io/controller-runtime"
)

var (
	// ErrCABundleNotFound is returned when CA bundle secret is not found
	ErrCABundleNotFound = errors.New("CA bundle secret not found")
	// ErrKeystoneNotReady is returned when Keystone client cannot be created
	ErrKeystoneNotReady = errors.New("keystone client not ready - password secret unavailable")
)

func getClient(
	ctx context.Context,
	h *helper.Helper,
	keystoneAPI *keystonev1.KeystoneAPI,
) (*openstack.OpenStack, ctrl.Result, error) {
	// get internal endpoint as authurl from keystone instance
	authURL, err := keystoneAPI.GetEndpoint(endpoint.EndpointInternal)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	parsedAuthURL, err := url.Parse(authURL)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	tlsConfig := &openstack.TLSConfig{}
	if parsedAuthURL.Scheme == "https" {
		caCert, ctrlResult, err := secret.GetDataFromSecret(
			ctx,
			h,
			keystoneAPI.Spec.TLS.CaBundleSecretName,
			time.Duration(10)*time.Second,
			tls.InternalCABundleKey)
		if err != nil {
			return nil, ctrl.Result{}, err
		}
		if (ctrlResult != ctrl.Result{}) {
			return nil, ctrl.Result{}, fmt.Errorf("%w: %s", ErrCABundleNotFound, keystoneAPI.Spec.TLS.CaBundleSecretName)
		}

		tlsConfig = &openstack.TLSConfig{
			CACerts: []string{
				caCert,
			},
		}
	}

	// get the password of the admin user from Spec.Secret
	// using PasswordSelectors.Admin
	authPassword, ctrlResult, err := secret.GetDataFromSecret(
		ctx,
		h,
		keystoneAPI.Spec.Secret,
		time.Duration(10)*time.Second,
		keystoneAPI.Spec.PasswordSelectors.Admin)
	if err != nil {
		return nil, ctrl.Result{}, err
	}
	if (ctrlResult != ctrl.Result{}) {
		// TODO callers are ignoring these return values
		// It means that this function can return a nil client when keystone is not fully initialized
		return nil, ctrlResult, nil
	}

	authOpts := openstack.AuthOpts{
		AuthURL:  authURL,
		Username: keystoneAPI.Spec.AdminUser,
		Password: authPassword,
		// The Domain of the user is always Default
		DomainName: "Default",
		TenantName: keystoneAPI.Spec.AdminProject,
		Region:     keystoneAPI.Spec.Region,
		TLS:        tlsConfig,
	}

	os, err := openstack.NewOpenStack(
		ctx,
		h.GetLogger(),
		authOpts,
	)
	if err != nil {
		return nil, ctrl.Result{}, err
	}

	return os, ctrl.Result{}, nil
}

// GetAdminServiceClient - get a client for the "admin" tenant
func GetAdminServiceClient(
	ctx context.Context,
	h *helper.Helper,
	keystoneAPI *keystonev1.KeystoneAPI,
) (*openstack.OpenStack, ctrl.Result, error) {
	return getClient(ctx, h, keystoneAPI)
}

// GetOpenstackClient returns an openstack admin service client object
func GetOpenstackClient(
	ctx context.Context,
	ns string,
	h *helper.Helper,
) (*openstack.OpenStack, error) {
	keystoneAPI, err := keystonev1.GetKeystoneAPI(ctx, h, ns, map[string]string{})
	if err != nil {
		return nil, err
	}
	o, ctrlResult, err := GetAdminServiceClient(ctx, h, keystoneAPI)
	if err != nil {
		return nil, err
	}
	if (ctrlResult != ctrl.Result{}) {
		return nil, ErrKeystoneNotReady
	}
	if o == nil {
		return nil, ErrKeystoneNotReady
	}
	return o, nil
}

// GetDNSClient returns a Gophercloud service client for Designate DNS API
func GetDNSClient(o *openstack.OpenStack) (*gophercloud.ServiceClient, error) {
	endpointOpts := gophercloud.EndpointOpts{
		Region:       o.GetRegion(),
		Availability: gophercloud.AvailabilityInternal,
	}
	return gophercloudopenstack.NewDNSV2(o.GetOSClient().ProviderClient, endpointOpts)
}
