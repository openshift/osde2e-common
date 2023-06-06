package ocm

import (
	"context"
	"fmt"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
)

type Environment string

const (
	Production  Environment = "https://api.openshift.com"
	Stage       Environment = "https://api.stage.openshift.com"
	Integration Environment = "https://api.integration.openshift.com"
)

type Client struct {
	*ocmsdk.Connection
}

func New(ctx context.Context, token string, environment Environment) (*Client, error) {
	connection, err := ocmsdk.NewConnectionBuilder().
		URL(string(environment)).
		Tokens(token).
		BuildContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create ocm connection: %w", err)
	}

	return &Client{connection}, nil
}
