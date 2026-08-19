package openshift

import (
	"context"
	"fmt"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

const (
	timeoutLoggerKey = "timeout"
)

// OSDClusterHealthy waits for the cluster to be in a healthy "ready" state
// by confirming every ClusterOperator is Available, not Degraded, and not
// Progressing.
//
// This previously waited on the osd-cluster-ready Job, but that Job was
// removed cluster-side (see openshift/managed-cluster-config#ROSAENG-1342):
// it duplicated the ClusterOperator Progressing=false signal checked here,
// and nothing in OCM/OSDFM consumed its completion signal. Waiting on a Job
// that no longer exists caused this check to time out on every cluster, and
// the subsequent GetJobLogs call to panic when no pods were ever created for
// it.
func (c *Client) OSDClusterHealthy(ctx context.Context, reportDir string, timeout time.Duration) error {
	c.log.Info("Waiting for cluster operators to report healthy", timeoutLoggerKey, timeout.Round(time.Second).String())

	var unhealthy []string
	err := wait.For(func(ctx context.Context) (bool, error) {
		operators := new(configv1.ClusterOperatorList)
		if err := c.List(ctx, operators); err != nil {
			if IsRetryableAPIError(err) {
				c.log.Error(err, "failed to list cluster operators, retrying")
				return false, nil
			}
			return false, err
		}

		if len(operators.Items) == 0 {
			return false, nil
		}

		unhealthy = unhealthy[:0]
		for _, operator := range operators.Items {
			if !clusterOperatorHealthy(operator) {
				unhealthy = append(unhealthy, operator.Name)
			}
		}

		if len(unhealthy) > 0 {
			c.log.Info(fmt.Sprintf("waiting for %d cluster operator(s) to become healthy", len(unhealthy)), "unhealthy_operators", unhealthy)
			return false, nil
		}

		return true, nil
	}, wait.WithTimeout(timeout))
	if err != nil {
		return fmt.Errorf("cluster operators failed to report healthy within %s (last unhealthy: %v): %w", timeout, unhealthy, err)
	}

	c.log.Info("Cluster operators reported healthy!")

	return nil
}

// clusterOperatorHealthy returns true if the operator is Available, not
// Degraded, and not Progressing.
func clusterOperatorHealthy(operator configv1.ClusterOperator) bool {
	var available, degraded, progressing bool
	for _, condition := range operator.Status.Conditions {
		switch condition.Type {
		case configv1.OperatorAvailable:
			available = condition.Status == configv1.ConditionTrue
		case configv1.OperatorDegraded:
			degraded = condition.Status == configv1.ConditionTrue
		case configv1.OperatorProgressing:
			progressing = condition.Status == configv1.ConditionTrue
		}
	}
	return available && !degraded && !progressing
}

// HCPClusterHealthy waits for the cluster to be in a health "ready" state
// by confirming nodes are available
func (c *Client) HCPClusterHealthy(ctx context.Context, computeNodes int, timeout time.Duration) error {
	c.log.Info("Waiting for hosted control plane cluster to healthy", timeoutLoggerKey, timeout.Round(time.Second).String())

	err := wait.For(func(ctx context.Context) (bool, error) {
		var nodes corev1.NodeList
		err := c.List(ctx, &nodes)
		if err != nil {
			if os.IsTimeout(err) {
				c.log.Error(err, "timeout occurred contacting api server")
				return false, nil
			}
			return false, err
		}

		if len(nodes.Items) == 0 {
			return false, nil
		}

		for _, node := range nodes.Items {
			for _, condition := range node.Status.Conditions {
				if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
					return false, nil
				}
			}
		}

		return len(nodes.Items) == computeNodes, nil
	}, wait.WithTimeout(timeout))
	if err != nil {
		return fmt.Errorf("hosted control plane cluster health check failed: %w", err)
	}

	c.log.Info("Hosted control plane cluster health check finished successfully!")

	return nil
}
