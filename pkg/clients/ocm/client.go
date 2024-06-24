package ocm

import (
	"context"
	"fmt"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
)

type Environment string

const (
	Production         Environment = "https://api.openshift.com"
	Stage              Environment = "https://api.stage.openshift.com"
	Integration        Environment = "https://api.integration.openshift.com"
	FedRampProduction  Environment = "https://api.openshiftusgov.com"
	FedRampStage       Environment = "https://api.stage.openshiftusgov.com"
	FedRampIntegration Environment = "https://api.int.openshiftusgov.com"
	fedrampTokenURL    string      = "https://sso.int.openshiftusgov.com/realms/redhat-external/protocol/openid-connect/token"
)

type Client struct {
	*ocmsdk.Connection
}

func New(ctx context.Context,
	token string,
	clientID string,
	clientSecret string,
	environment Environment,
) (*Client, error) {
	connectionBuilder := ocmsdk.NewConnectionBuilder().URL(string(environment))

	if clientID != "" && clientSecret != "" {
		connectionBuilder.Client(clientID, clientSecret).
			TokenURL(fedrampTokenURL)
	} else {
		connectionBuilder.Tokens(token)
	}

	connection, err := connectionBuilder.BuildContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create ocm connection: %w", err)
	}

	return &Client{connection}, nil
}
