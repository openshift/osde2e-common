package assertions

import (
	"context"

	"github.com/onsi/gomega"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	corev1 "k8s.io/api/core/v1"
)

// EventuallyConfigMap is a gomega async assertion that can be used with the
// standard or custom gomega matchers
// Polls the resource every 30 seconds until success or timeout defined by POLLING_TIMEOUT
//
//	EventuallyConfigMap(ctx, client, configMapName, namespace).Should(Not(BeNil()), "config map %s should exist", configMapName)
func EventuallyConfigMap(ctx context.Context, client *openshift.Client, name, namespace string) gomega.AsyncAssertion {
	return gomega.Eventually(ctx, func(ctx context.Context) (*corev1.ConfigMap, error) {
		var configMap corev1.ConfigMap
		err := client.Get(ctx, name, namespace, &configMap)
		return &configMap, err
	})
}
