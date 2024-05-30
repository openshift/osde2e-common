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
	"k8s.io/client-go/kubernetes"
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
