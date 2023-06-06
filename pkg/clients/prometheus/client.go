package prometheus

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	"github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
)

type Client struct {
	prometheus prometheusv1.API
}

// TODO: should we use thanos querier instead?
func New(ctx context.Context, client *openshift.Client) (*Client, error) {
	var route routev1.Route
	if err := client.Get(ctx, "prometheus-k8s", "openshift-monitoring", &route); err != nil {
		return nil, fmt.Errorf("unable to find prometheus route: %w", err)
	}
	address := "https://" + route.Spec.Host

	var secretList corev1.SecretList
	if err := client.WithNamespace("openshift-monitoring").List(ctx, &secretList); err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	var bearerToken string
	for _, secret := range secretList.Items {
		if strings.HasPrefix(secret.Name, "prometheus-k8s-token") {
			bearerToken = string(secret.Data[corev1.ServiceAccountTokenKey])
			break
		}
	}
	if len(bearerToken) == 0 {
		return nil, fmt.Errorf("failed to find token secret for prometheus-k8s serviceaccount")
	}

	// TODO: can this be done differently?
	cfg := api.Config{
		Address: address,
		RoundTripper: &http.Transport{
			Proxy: func(request *http.Request) (*url.URL, error) {
				request.Header.Add("Authorization", "Bearer "+bearerToken)
				return http.ProxyFromEnvironment(request)
			},
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
			TLSHandshakeTimeout: 10 * time.Second,
		},
	}

	promClient, err := api.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create prometheus client: %w", err)
	}

	return &Client{prometheusv1.NewAPI(promClient)}, nil
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
