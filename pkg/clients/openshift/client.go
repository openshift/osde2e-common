package openshift

import (
	"fmt"

	"github.com/openshift/api"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient/conf"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
)

type Client struct {
	*resources.Resources
}

func New() (*Client, error) {
	return NewFromKubeconfig("")
}

func NewFromKubeconfig(filename string) (*Client, error) {
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
	return &Client{client}, nil
}

// Impersonate returns a copy of the client with a new ImpersonationConfig
// established on the underlying client, acting as the provided user
//
//	backplaneUser, _ := oc.Impersonate("test-user@redhat.com", "dedicated-admins")
func (c *Client) Impersonate(user string, groups ...string) (*Client, error) {
	if user != "" {
		// these groups are required for impersonating a user
		groups = append(groups, "system:authenticated", "system:authenticated:oauth")
	}

	client := *c
	newRestConfig := rest.CopyConfig(c.Resources.GetConfig())
	newRestConfig.Impersonate = rest.ImpersonationConfig{UserName: user, Groups: groups}
	newResources, err := resources.New(newRestConfig)
	if err != nil {
		return nil, err
	}
	client.Resources = newResources

	if err = api.Install(client.GetScheme()); err != nil {
		return nil, fmt.Errorf("unable to register openshift api schemes: %w", err)
	}

	return &client, nil
}
