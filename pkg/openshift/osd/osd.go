package osd

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	ocmclient "github.com/openshift/osde2e-common/pkg/clients/ocm"
)

// Provider is a openshift dedicated "osd" provider
type Provider struct {
	*ocmclient.Client
	log            logr.Logger
	ocmEnvironment ocmclient.Environment
}

// providerError represents the provider custom error
type providerError struct {
	err error
}

// Error returns the formatted error message when providerError is invoked
func (o *providerError) Error() string {
	return fmt.Sprintf("failed to construct osd provider: %v", o.err)
}

// New handles constructing the osd provider which creates a connection
// to openshift cluster manager "ocm". It is the callers responsibility
// to close the ocm connection when they are finished (defer provider.Connection.Close())
func New(ctx context.Context, token string, ocmEnvironment ocmclient.Environment, logger logr.Logger) (*Provider, error) {
	if ocmEnvironment == "" || token == "" {
		return nil, &providerError{err: fmt.Errorf("some parameters are undefined, unable to construct osd provider")}
	}

	ocmClient, err := ocmclient.New(ctx, token, ocmEnvironment)
	if err != nil {
		return nil, &providerError{err: err}
	}

	return &Provider{
		Client:         ocmClient,
		log:            logger,
		ocmEnvironment: ocmEnvironment,
	}, nil
}
