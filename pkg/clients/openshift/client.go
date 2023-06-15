package openshift

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/openshift/api"
	"sigs.k8s.io/e2e-framework/klient/conf"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
)

type Client struct {
	*resources.Resources
	log logr.Logger
}

func New(logger logr.Logger) (*Client, error) {
	return NewFromKubeconfig("", logger)
}

func NewFromKubeconfig(filename string, logger logr.Logger) (*Client, error) {
	cfg, err := conf.New(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to get kubernetes config: %w", err)
	}
	client, err := resources.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to created dynamic client: %w", err)
	}
	if err = api.Install(client.GetScheme()); err != nil {
		return nil, fmt.Errorf("unable to register openshift api schemes: %w", err)
	}
	return &Client{client, logger}, nil
}
