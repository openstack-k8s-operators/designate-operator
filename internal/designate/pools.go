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
	"io"
	"regexp"
	"strings"
	"time"

	designatev1beta1 "github.com/openstack-k8s-operators/designate-operator/api/v1beta1"
	"github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	"github.com/openstack-k8s-operators/lib-common/modules/common/job"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// PoolListJobTimeout is the maximum time to wait for pool list job completion
	PoolListJobTimeout = 60 * time.Second

	// PoolRetryCooldown is the minimum time between retry attempts that launch a
	// pool-list or pool-update Job, independent of the reconciler's own
	// RequeueAfter. RequeueAfter only schedules the *next* reconcile; it doesn't
	// stop watches on other resources (Secrets, Jobs, StatefulSets) from
	// triggering earlier reconciles in the meantime. Without this cooldown, two
	// controllers retrying against the same unconverged state can trigger each
	// other's watches and launch a new Job on almost every reconcile.
	PoolRetryCooldown = 30 * time.Second
)

var (
	// ErrNoPoolsFound is returned when no pools are found
	ErrNoPoolsFound = errors.New("no pools found")
	// ErrParsePoolOutput is returned when pool output cannot be parsed
	ErrParsePoolOutput = errors.New("failed to parse pool output")
	// ErrPoolListJobNotComplete is returned when the pool list job hasn't finished
	ErrPoolListJobNotComplete = errors.New("pool list job not complete yet")
	// ErrPoolListJobFailed is returned when the pool list job execution fails
	ErrPoolListJobFailed = errors.New("failed to execute pool list job")
	// ErrNoPodFoundForJob is returned when no pod is found for a job
	ErrNoPodFoundForJob = errors.New("no pod found for job")
)

// PoolInfo holds pool database information
type PoolInfo struct {
	ID   string
	Name string
}

// ParsePoolListOutput parses the output of 'designate-manage pool update --dry-run'
// Expected format: "Update Pool: <Pool id:'794ccc2c-d751-44fe-b57f-8894c9f5c842' name:'default'>"
func ParsePoolListOutput(output string) ([]PoolInfo, error) {
	// Regular expression to match: id:'<uuid>' name:'<name>'
	re := regexp.MustCompile(`id:'([^']+)'\s+name:'([^']+)'`)

	var pools []PoolInfo
	scanner := bufio.NewScanner(strings.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			pools = append(pools, PoolInfo{
				ID:   matches[1],
				Name: matches[2],
			})
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParsePoolOutput, err)
	}

	if len(pools) == 0 {
		return nil, ErrNoPoolsFound
	}

	return pools, nil
}

// GetPoolNameToIDMap retrieves all pools using designate-manage and returns a map of name->ID
func GetPoolNameToIDMap(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	instance *designatev1beta1.Designate,
) (map[string]string, error) {
	pools, err := ListPoolsFromJob(ctx, h, namespace, instance)
	if err != nil {
		return nil, err
	}

	poolMap := make(map[string]string)
	for _, pool := range pools {
		poolMap[pool.Name] = pool.ID
	}

	return poolMap, nil
}

// ListPoolsFromJob retrieves pool information by running designate-manage pool update --dry-run as a Kubernetes Job
// This replaces the old galera-based approach with a proper job-based solution
func ListPoolsFromJob(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	instance *designatev1beta1.Designate,
) ([]PoolInfo, error) {
	// Create job definition following the same pattern as PoolUpdateJob
	labels := map[string]string{"app": "designate", "job-type": "pool-list"}
	annotations := map[string]string{}

	jobDef := PoolListJob(instance, labels, annotations)

	// Create and execute the job using lib-common job helper
	poolListJob := job.NewJob(
		jobDef,
		"pool-list", // jobType
		false,       // preserve - don't preserve this transient job
		PoolListJobTimeout,
		"", // hash - not using hash for this operation
	)

	// Execute the job
	ctrlResult, err := poolListJob.DoJob(ctx, h)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrPoolListJobFailed, err)
	}
	if (ctrlResult != ctrl.Result{}) {
		return nil, ErrPoolListJobNotComplete
	}

	// Get the pod created by the job to retrieve logs
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(namespace),
		client.MatchingLabels(map[string]string{"job-name": jobDef.Name}),
	}
	err = h.GetClient().List(ctx, podList, listOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list pods for job %s: %w", jobDef.Name, err)
	}

	if len(podList.Items) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrNoPodFoundForJob, jobDef.Name)
	}

	// Get logs from the pod
	pod := podList.Items[0]
	containerName := jobDef.Spec.Template.Spec.Containers[0].Name
	logs, err := getPodLogs(ctx, h, namespace, pod.Name, containerName)
	if err != nil {
		return nil, fmt.Errorf("failed to get logs from pod %s: %w", pod.Name, err)
	}

	// Parse the output
	pools, err := ParsePoolListOutput(logs)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pool output: %w", err)
	}

	return pools, nil
}

// getPodLogs retrieves logs from a pod container
func getPodLogs(
	ctx context.Context,
	h *helper.Helper,
	namespace string,
	podName string,
	containerName string,
) (string, error) {
	req := h.GetKClient().CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get log stream for pod %s: %w", podName, err)
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			err = fmt.Errorf("failed to close log stream: %w", closeErr)
		}
	}()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, stream)
	if err != nil {
		return "", fmt.Errorf("failed to read logs from pod %s: %w", podName, err)
	}

	return buf.String(), nil
}

// RetryCooldownElapsed checks whether at least `cooldown` has passed since the
// RFC3339 timestamp stored under lastAttemptKey in statusHash. If the cooldown
// has elapsed (or no prior attempt is recorded), it stamps the current time
// into statusHash and returns ready=true, so the caller should proceed and the
// caller's own status patch will persist the new timestamp. If not enough time
// has passed, it returns ready=false and the remaining wait, so the caller
// should return ctrl.Result{RequeueAfter: wait} without repeating the guarded
// work (e.g. launching a pool-list/pool-update Job).
func RetryCooldownElapsed(statusHash map[string]string, lastAttemptKey string, cooldown time.Duration) (wait time.Duration, ready bool) {
	if last, ok := statusHash[lastAttemptKey]; ok {
		if t, err := time.Parse(time.RFC3339, last); err == nil {
			if elapsed := time.Since(t); elapsed < cooldown {
				return cooldown - elapsed, false
			}
		}
	}
	statusHash[lastAttemptKey] = time.Now().UTC().Format(time.RFC3339)
	return 0, true
}
