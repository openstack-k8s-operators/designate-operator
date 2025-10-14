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

package controllers

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// ObjectListFunc is a function type for listing objects that match a given field selector.
// It returns a slice of ObjectMeta for the matching objects, allowing callers to create
// reconcile requests without needing full object details.
type ObjectListFunc func(ctx context.Context, cl client.Client, field string, src client.Object, log *logr.Logger) ([]metav1.ObjectMeta, error)

// CreateRequestsFromObjectUpdates is a generic helper function that creates reconcile requests
// for all objects that reference a given source object. It iterates through configured watch fields
// and uses the provided ObjectListFunc to locate dependent objects.
// This function encapsulates the boilerplate pattern used across multiple controllers to handle
// changes in watched resources (secrets, configmaps, topology, etc.).
func CreateRequestsFromObjectUpdates(
	ctx context.Context,
	cl client.Client,
	src client.Object,
	watchFields []string,
	listFunc ObjectListFunc,
	log *logr.Logger,
) []reconcile.Request {
	requests := []reconcile.Request{}
	for _, field := range watchFields {
		objs, err := listFunc(ctx, cl, field, src, log)
		if err != nil {
			return requests
		}
		for _, obj := range objs {
			log.Info(fmt.Sprintf("input source %s changed, reconcile: %s - %s",
				src.GetName(), obj.GetName(), obj.GetNamespace()))
			requests = append(requests,
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      obj.GetName(),
						Namespace: obj.GetNamespace(),
					},
				},
			)
		}
	}
	return requests
}
