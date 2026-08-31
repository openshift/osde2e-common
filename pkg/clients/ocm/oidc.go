package ocm

import (
	"context"
	"fmt"

	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

const oidcConfigPageSize = 100

// ListOIDCConfigs returns OCM OIDC configs keyed by ID.
func ListOIDCConfigs(ctx context.Context, conn *ocmsdk.Connection) (map[string]*cmv1.OidcConfig, error) {
	if conn == nil {
		return nil, fmt.Errorf("ocm connection is required")
	}

	byID := make(map[string]*cmv1.OidcConfig)
	page := 1
	collected := 0
	for {
		resp, err := conn.ClustersMgmt().V1().OidcConfigs().List().
			Page(page).
			Size(oidcConfigPageSize).
			SendContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("list oidc configs: %w", err)
		}
		if items := resp.Items(); items != nil {
			for _, cfg := range items.Slice() {
				if id := cfg.ID(); id != "" {
					byID[id] = cfg
				}
			}
		}
		collected += resp.Size()
		if collected >= resp.Total() || resp.Size() == 0 {
			break
		}
		page++
	}
	return byID, nil
}

// ListOIDCConfigs returns OCM OIDC configs keyed by ID.
func (c *Client) ListOIDCConfigs(ctx context.Context) (map[string]*cmv1.OidcConfig, error) {
	if c == nil || c.Connection == nil {
		return nil, fmt.Errorf("ocm client is not connected")
	}
	return ListOIDCConfigs(ctx, c.Connection)
}

// ClusterOIDCSecretARN returns the CCS-hosted unmanaged ROSA OIDC private-key
// ARN for a cluster. An empty string means the cluster has no such secret
// (no STS OIDC config, or a Red Hat-managed OIDC config).
//
// oidcByID is the result of ListOIDCConfigs. Cluster GET often includes only
// the OIDC config ID; SecretArn is filled from that map. A nil map is treated
// as "lookup unavailable" and fails closed when the cluster needs a lookup.
func ClusterOIDCSecretARN(cluster *cmv1.Cluster, oidcByID map[string]*cmv1.OidcConfig) (string, error) {
	if cluster == nil {
		return "", nil
	}
	awsCfg, ok := cluster.GetAWS()
	if !ok || awsCfg == nil {
		return "", nil
	}
	sts, ok := awsCfg.GetSTS()
	if !ok || sts == nil {
		return "", nil
	}
	oidc, ok := sts.GetOidcConfig()
	if !ok || oidc == nil {
		return "", nil
	}
	if arn := oidc.SecretArn(); arn != "" {
		return arn, nil
	}

	id := oidc.ID()
	if id == "" {
		return "", nil
	}
	if oidcByID == nil {
		return "", fmt.Errorf("cluster %s oidc config %s: oidc config list not provided", cluster.ID(), id)
	}

	full, ok := oidcByID[id]
	if !ok {
		if oidc.Managed() {
			return "", nil
		}
		return "", fmt.Errorf("cluster %s oidc config %s not found in OCM oidc_configs list", cluster.ID(), id)
	}
	if arn := full.SecretArn(); arn != "" {
		return arn, nil
	}
	if full.Managed() {
		return "", nil
	}
	return "", fmt.Errorf("cluster %s oidc config %s has no secret_arn", cluster.ID(), id)
}

// ClusterOIDCSecretARNs resolves CCS-hosted unmanaged ROSA OIDC private-key
// ARNs for the given clusters. The returned map is never nil.
func ClusterOIDCSecretARNs(clusters []*cmv1.Cluster, oidcByID map[string]*cmv1.OidcConfig) (map[string]bool, error) {
	arns := make(map[string]bool)
	for _, cluster := range clusters {
		arn, err := ClusterOIDCSecretARN(cluster, oidcByID)
		if err != nil {
			return nil, err
		}
		if arn != "" {
			arns[arn] = true
		}
	}
	return arns, nil
}

// OIDCSecretARNs lists OCM OIDC configs and resolves CCS-hosted unmanaged ROSA
// OIDC private-key ARNs for the given clusters. An empty cluster list returns
// an empty map without calling OCM. The returned map is never nil.
func OIDCSecretARNs(ctx context.Context, conn *ocmsdk.Connection, clusters []*cmv1.Cluster) (map[string]bool, error) {
	arns := make(map[string]bool)
	if len(clusters) == 0 {
		return arns, nil
	}
	oidcByID, err := ListOIDCConfigs(ctx, conn)
	if err != nil {
		return nil, err
	}
	return ClusterOIDCSecretARNs(clusters, oidcByID)
}

// OIDCSecretARNs lists OCM OIDC configs and resolves CCS-hosted unmanaged ROSA
// OIDC private-key ARNs for the given clusters.
func (c *Client) OIDCSecretARNs(ctx context.Context, clusters []*cmv1.Cluster) (map[string]bool, error) {
	if c == nil || c.Connection == nil {
		return nil, fmt.Errorf("ocm client is not connected")
	}
	return OIDCSecretARNs(ctx, c.Connection, clusters)
}
