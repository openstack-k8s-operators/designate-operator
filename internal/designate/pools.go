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
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	// ErrGaleraNotReady is returned when galera pods are not ready
	ErrGaleraNotReady = errors.New("galera not ready: no galera pods found")
	// ErrNoPoolsFound is returned when no pools are found in the database
	ErrNoPoolsFound = errors.New("no pools found in database")
)

// PoolInfo holds pool database information
type PoolInfo struct {
	ID   string
	Name string
}

// GetPoolNameToIDMap retrieves all pools from database and returns a map of name->ID
func GetPoolNameToIDMap(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
) (map[string]string, error) {
	pools, err := ListPoolsFromDatabase(ctx, h, namespace)
	if err != nil {
		return nil, err
	}

	poolMap := make(map[string]string)
	for _, pool := range pools {
		poolMap[pool.Name] = pool.ID
	}

	return poolMap, nil
}

// ListPoolsFromDatabase queries the Designate database directly for pool information
// Using galera this way is a bad practice, but it was implmeneted by Claude and worked.
// TODO(oschwart): replace it with a designate-manage approach
func ListPoolsFromDatabase(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
) ([]PoolInfo, error) {
	// Find a galera pod
	galeraCmd := "SELECT id, name FROM pools ORDER BY name"

	output, err := execDatabaseQuery(ctx, h, namespace, galeraCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to query pools from database: %w", err)
	}

	// Parse output (tab-separated: id\tname)
	var pools []PoolInfo
	scanner := bufio.NewScanner(strings.NewReader(output))
	// Skip header line (id\tname) - we don't need to check the result
	scanner.Scan()
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\t")
		if len(parts) >= 2 {
			pools = append(pools, PoolInfo{
				ID:   parts[0],
				Name: parts[1],
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(pools) == 0 {
		return nil, ErrNoPoolsFound
	}

	return pools, nil
}

// execDatabaseQuery executes a SQL query on the designate database via galera pod
func execDatabaseQuery(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	query string,
) (string, error) {
	// Find a running galera pod
	podList := &corev1.PodList{}
	err := h.GetClient().List(ctx, podList, &client.ListOptions{
		Namespace: namespace,
		LabelSelector: labels.SelectorFromSet(map[string]string{
			"galera/name": "openstack",
		}),
	})
	if err != nil {
		return "", fmt.Errorf("failed to list galera pods: %w", err)
	}

	var galeraPodName string
	for _, pod := range podList.Items {
		if pod.Status.Phase == corev1.PodRunning {
			galeraPodName = pod.Name
			break
		}
	}

	if galeraPodName == "" {
		return "", ErrGaleraNotReady
	}

	// Execute mysql command
	command := []string{
		"mysql", "-u", "root", "-D", "designate", "-e", query,
	}

	return execCommandInPodWithOutput(ctx, h, namespace, galeraPodName, "galera", command)
}

// execCommandInPodWithOutput executes a command in a pod and returns the output
func execCommandInPodWithOutput(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	podName string,
	containerName string,
	command []string,
) (string, error) {
	// Get the Kubernetes clientset
	kclient := h.GetKClient()

	// Create exec request
	req := kclient.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(podName).
		Namespace(namespace).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: containerName,
			Command:   command,
			Stdout:    true,
			Stderr:    true,
		}, scheme.ParameterCodec)

	// Get config - works both in-cluster and locally (via kubeconfig)
	config, err := ctrl.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get cluster config: %w", err)
	}

	exec, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
	if err != nil {
		return "", fmt.Errorf("failed to create executor: %w", err)
	}

	// Capture output
	var stdout, stderr strings.Builder
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err != nil {
		return "", fmt.Errorf("command execution failed: %w, stderr: %s", err, stderr.String())
	}

	return stdout.String(), nil
}
