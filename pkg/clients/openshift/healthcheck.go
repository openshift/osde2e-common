package openshift

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
)

const (
	osdClusterReadyNamespace = "openshift-monitoring"
	jobNameLoggerKey         = "jobName"
	timeoutLoggerKey         = "timeout"
)

// OSDClusterHealthy waits for the cluster to be in a healthy "ready" state
// by confirming the osd-ready-job finishes successfully
func (c *Client) OSDClusterHealthy(ctx context.Context, jobName, reportDir string, timeout time.Duration) error {
	var job batchv1.Job

	err := c.Get(ctx, jobName, osdClusterReadyNamespace, &job)
	if err != nil {
		return fmt.Errorf("failed to get existing %s job %v", jobName, err)
	}

	c.log.Info("Wait for cluster job to finish", jobNameLoggerKey, jobName, timeoutLoggerKey, timeout)

	err = wait.For(conditions.New(c.Resources).JobCompleted(&job), wait.WithTimeout(timeout))
	if err != nil {
		var pods corev1.PodList
		if err = c.List(ctx, &pods, resources.WithLabelSelector(labels.FormatLabels(map[string]string{"job-name": jobName}))); err != nil {
			return errors.New("failed to get pods")
		}

		if len(pods.Items) != 1 {
			return fmt.Errorf("no pods found for job %s", jobName)
		}
		podName := pods.Items[0].GetName()

		clientSet, err := kubernetes.NewForConfig(c.GetConfig())
		if err != nil {
			return fmt.Errorf("failed to create kubernetes clientset: %v", err)
		}
		request := clientSet.CoreV1().Pods(osdClusterReadyNamespace).GetLogs(podName, &corev1.PodLogOptions{})
		logData, err := request.DoRaw(ctx)
		if err != nil {
			return fmt.Errorf("failed to get pod %s logs: %v", podName, err)
		}

		if err = os.WriteFile(fmt.Sprintf("%s/%s.log", reportDir, jobName), logData, os.FileMode(0o644)); err != nil {
			return fmt.Errorf("failed to write pod %s logs to file: %v", podName, err)
		}

		return fmt.Errorf("%s failed to complete in desired time/health checks have failed", jobName)
	}

	c.log.Info("Cluster job finished successfully!", jobNameLoggerKey, jobName)

	return nil
}

// HCPClusterHealthy waits for the cluster to be in a health "ready" state
// by confirming nodes are available
func (c *Client) HCPClusterHealthy(ctx context.Context, timeout time.Duration) error {
	c.log.Info("Wait for hosted control plane cluster to healthy", timeoutLoggerKey, timeout)

	err := wait.For(func() (bool, error) {
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

		// TODO: Compare with number of nodes cluster is deployed with
		return true, nil
	}, wait.WithTimeout(timeout))
	if err != nil {
		return fmt.Errorf("hosted control plane cluster health check failed: %v", err)
	}

	c.log.Info("Hosted control plane cluster health check finished successfully!")

	return nil
}
