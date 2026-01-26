package prometheus

import (
	"context"
	"errors"
	"fmt"
	"time"

	routev1client "github.com/openshift/client-go/route/clientset/versioned"
	"github.com/openshift/library-go/test/library/metrics"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// Prometheus configuration for OpenShift
	prometheusNamespace      = "openshift-monitoring"
	prometheusServiceAccount = "prometheus-k8s"
	prometheusTokenDuration  = 1 * time.Hour
)

type Client struct {
	prometheus prometheusv1.API
}

// TODO: should we use thanos querier instead?
func New(ctx context.Context, client *openshift.Client) (*Client, error) {
	cfg := client.GetConfig()
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	routeClient, err := routev1client.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}

	prometheus, err := metrics.NewPrometheusClient(ctx, kubeClient, routeClient)
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}

	return &Client{prometheus: prometheus}, nil
}

func (c *Client) GetClient() prometheusv1.API {
	return c.prometheus
}

func (c *Client) InstantQuery(ctx context.Context, query string) (model.Vector, error) {
	result, warnings, err := c.prometheus.Query(ctx, query, time.Now())
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}

	// TODO: do something with these
	_ = warnings

	vector, ok := result.(model.Vector)
	if !ok {
		return nil, errors.New("failed to convert result to a Vector object")
	}

	return vector, nil
}

// GetPrometheusToken retrieves a token for the prometheus-k8s service account using TokenRequest API.
// This implementation follows the pattern from openshift/library-go/test/library/metrics/query.go.
// This is the modern, recommended approach for Kubernetes 1.24+.
func GetPrometheusToken(ctx context.Context, client *openshift.Client) (string, error) {
	// Get config and create Kubernetes client
	cfg := client.GetConfig()
	kubeClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create token request - matches library-go pattern
	expirationSeconds := int64(prometheusTokenDuration / time.Second)
	req, err := kubeClient.CoreV1().ServiceAccounts(prometheusNamespace).CreateToken(ctx, prometheusServiceAccount,
		&authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{ExpirationSeconds: &expirationSeconds},
		}, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("error requesting token for service account %s/%s: %w", prometheusNamespace, prometheusServiceAccount, err)
	}

	if req.Status.Token == "" {
		return "", fmt.Errorf("received empty token for service account %s/%s", prometheusNamespace, prometheusServiceAccount)
	}

	return req.Status.Token, nil
}
