package osd

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

type CloudProvider string

var (
	CloudProviderAWS CloudProvider = "aws"
	CloudProviderGCP CloudProvider = "gcp"
)

type CreateClusterOptions struct {
	SkipHealthCheck bool
	ArtifactDir     string

	Addons             []string
	CCS                bool
	ChannelGroup       string
	CloudProvider      CloudProvider
	ClusterName        string
	ComputeMachineType string
	ComputeNodeCount   int
	HTTPProxy          string
	HTTPSProxy         string
	FlavorID           string
	MultiAZ            bool
	Properties         map[string]string
	Region             string
	Version            string

	CreateAWSClusterOptions *CreateAWSClusterOptions
	CreateGCPClusterOptions *CreateGCPClusterOptions

	InstallTimeout     time.Duration
	HealthCheckTimeout time.Duration
	ExpirationDuration time.Duration
}

type CreateAWSClusterOptions struct {
	AccountID       string
	AccessKeyID     string
	SecretAccessKey string
	SubnetIDs       []string
}

type CreateGCPClusterOptions struct {
	Type                    string
	ProjectID               string
	PrivateKey              string
	PrivateKeyID            string
	ClientEmail             string
	ClientID                string
	AuthURI                 string
	TokenURI                string
	AuthProviderX509CertURL string
	ClientX509CertURL       string
}

type DeleteClusterOptions struct {
	ClusterID       string
	WaitForDeletion bool
}

// CreateCluster creates an OSD cluster using the provided inputs
func (p *Provider) CreateCluster(ctx context.Context, options *CreateClusterOptions) (string, error) {
	options, err := p.validateCreateClusterOptions(options)
	if err != nil {
		return "", fmt.Errorf("invalid CreateClusterOptions: %w", err)
	}

	regionBuilder := cmv1.NewCloudRegion().ID(options.Region)
	newCluster := cmv1.NewCluster().
		CloudProvider(cmv1.NewCloudProvider().ID(string(options.CloudProvider))).
		Flavour(cmv1.NewFlavour().ID(options.FlavorID)).
		MultiAZ(options.MultiAZ).
		Name(options.ClusterName).
		Properties(options.Properties).
		Region(regionBuilder).
		Version(cmv1.NewVersion().ID(options.Version).ChannelGroup(options.ChannelGroup))

	if len(options.Addons) > 0 {
		addons := []*cmv1.AddOnInstallationBuilder{}
		for _, addon := range options.Addons {
			addons = append(addons, cmv1.NewAddOnInstallation().Addon(cmv1.NewAddOn().ID(addon)))
		}
		newCluster.Addons(cmv1.NewAddOnInstallationList().Items(addons...))
	}

	if options.ExpirationDuration > 0 {
		newCluster.ExpirationTimestamp(time.Now().Add(options.ExpirationDuration).UTC())
	}

	nodeBuilder := cmv1.NewClusterNodes().Compute(options.ComputeNodeCount)

	if options.CCS {
		newCluster.CCS(cmv1.NewCCS().Enabled(true))
		switch options.CloudProvider {
		case CloudProviderAWS:
			awsBuilder := cmv1.NewAWS().
				AccountID(options.CreateAWSClusterOptions.AccountID).
				AccessKeyID(options.CreateAWSClusterOptions.AccessKeyID).
				SecretAccessKey(options.CreateAWSClusterOptions.SecretAccessKey)

			if len(options.CreateAWSClusterOptions.SubnetIDs) > 0 {
				subnetIDs := options.CreateAWSClusterOptions.SubnetIDs
				awsBuilder.SubnetIDs(subnetIDs...)
				// TODO: do availability zone stuff
				awsProviderData, err := cmv1.NewCloudProviderData().AWS(awsBuilder).Region(regionBuilder).Build()
				if err != nil {
					return "", fmt.Errorf("failed to build CloudProviderData object: %w", err)
				}
				vpcsSearchResp, err := p.ClustersMgmt().V1().AWSInquiries().Vpcs().Search().Page(1).Size(-1).Body(awsProviderData).SendContext(ctx)
				if err != nil {
					return "", fmt.Errorf("unable to search for VPCs in AWS: %w", err)
				}

				// what in tarnation is going on here
				var availabilityZones []string
				for _, vpc := range vpcsSearchResp.Items().Slice() {
					for _, subnetwork := range vpc.AWSSubnets() {
						for _, subnetID := range subnetIDs {
							if subnetID == subnetwork.SubnetID() {
								availabilityZones = append(availabilityZones, subnetwork.AvailabilityZone())
							}
						}
					}
				}
				nodeBuilder.AvailabilityZones(availabilityZones...)
			}

			// TODO: why is proxy stuff nested here?
			// add proxy optionally

			newCluster.AWS(awsBuilder)
		case CloudProviderGCP:
			// set GCP options
			gcpBuilder := cmv1.NewGCP().
				Type(options.CreateGCPClusterOptions.Type).
				ProjectID(options.CreateGCPClusterOptions.ProjectID).
				PrivateKey(options.CreateGCPClusterOptions.PrivateKey).
				PrivateKeyID(options.CreateGCPClusterOptions.PrivateKeyID).
				ClientEmail(options.CreateGCPClusterOptions.ClientEmail).
				ClientID(options.CreateGCPClusterOptions.ClientID).
				AuthURI(options.CreateGCPClusterOptions.AuthURI).
				TokenURI(options.CreateGCPClusterOptions.TokenURI).
				AuthProviderX509CertURL(options.CreateGCPClusterOptions.AuthProviderX509CertURL).
				ClientX509CertURL(options.CreateGCPClusterOptions.ClientX509CertURL)

			newCluster.GCP(gcpBuilder)
		}
	}

	if options.MultiAZ {
		// Default to 9 nodes for MultiAZ
		nodeBuilder.Compute(9)
		if options.ComputeNodeCount > 0 {
			nodeBuilder.Compute(options.ComputeNodeCount)
		}
		newCluster.MultiAZ(options.MultiAZ)
	}

	if options.ComputeMachineType != "" {
		nodeBuilder.ComputeMachineType(cmv1.NewMachineType().ID(options.ComputeMachineType))
	}

	newCluster.Nodes(nodeBuilder)

	if len(options.Addons) > 0 {
		// TODO: this doesn't feel correct, is there a `New*` function to use?
		addons := []*cmv1.AddOnInstallationBuilder{}
		for _, addon := range options.Addons {
			addons = append(addons, cmv1.NewAddOnInstallation().Addon(cmv1.NewAddOn().ID(addon)))
		}
		newCluster.Addons(cmv1.NewAddOnInstallationList().Items(addons...))
	}

	body, err := newCluster.Build()
	if err != nil {
		return "", fmt.Errorf("unable to build cluster object: %w", err)
	}

	response, err := p.ClustersMgmt().V1().Clusters().Add().Body(body).SendContext(ctx)
	if err != nil {
		return "", fmt.Errorf("failed sending cluster creation body: %w", err)
	}

	cluster := response.Body()
	clusterID := cluster.ID()

	p.log.Info("Cluster created, waiting for installed state", "id", clusterID, "state", cluster.State())

	err = wait.For(func(ctx context.Context) (bool, error) {
		clusterResp, err := p.ClustersMgmt().V1().Clusters().Cluster(clusterID).Get().SendContext(ctx)
		if err != nil {
			return false, err
		}
		cluster = clusterResp.Body()
		if cluster.State() == cmv1.ClusterStateError || cluster.State() == cmv1.ClusterStateUninstalling {
			return false, fmt.Errorf("cluster %s is in a bad state %s", clusterID, cluster.State())
		}
		return cluster.State() == cmv1.ClusterStateReady, nil
	}, wait.WithTimeout(options.InstallTimeout), wait.WithInterval(30*time.Second), wait.WithContext(ctx))
	if err != nil {
		return "", fmt.Errorf("cluster never reached an installed state: %w", err)
	}

	p.log.Info("Cluster installed", "id", clusterID, "state", cluster.State())

	if !options.SkipHealthCheck {
		p.log.Info("Waiting for cluster to be healthy", "id", clusterID)
		kubeconfigFile, err := p.KubeconfigFile(ctx, cluster.ID(), os.TempDir())
		if err != nil {
			return clusterID, err
		}
		client, err := openshift.NewFromKubeconfig(kubeconfigFile, p.log)
		if err != nil {
			return clusterID, err
		}
		if err = client.OSDClusterHealthy(ctx, options.ArtifactDir, options.HealthCheckTimeout); err != nil {
			return clusterID, err
		}
	}

	return clusterID, nil
}

// DeleteCluster deletes a osd cluster using the provided inputs
func (p *Provider) DeleteCluster(ctx context.Context, options *DeleteClusterOptions) error {
	clusterClient := p.ClustersMgmt().V1().Clusters().Cluster(options.ClusterID)
	clusterGetResp, err := clusterClient.Get().SendContext(ctx)
	if err != nil {
		return fmt.Errorf("unable to get cluster %s: %w", options.ClusterID, err)
	}
	cluster := clusterGetResp.Body()

	if cluster.State() == cmv1.ClusterStateUninstalling {
		p.log.Info("Cluster is already uninstalling", "id", cluster.ID())
		return nil
	}

	_, err = clusterClient.Delete().SendContext(ctx)
	if err != nil {
		return fmt.Errorf("deleting cluster failed: %w", err)
	}

	if options.WaitForDeletion {
		// TODO: wait for cluster to be deleted
		return nil
	}

	return nil
}

// validateCreateClusterOptions verifies required options are set and sets defaults if undefined
func (p *Provider) validateCreateClusterOptions(options *CreateClusterOptions) (*CreateClusterOptions, error) {
	// TODO: validate cluster name

	if options.ArtifactDir == "" {
		options.ArtifactDir = os.TempDir()
	}

	if options.FlavorID == "" {
		options.FlavorID = "osd-4"
	}

	if options.ComputeNodeCount <= 0 {
		return options, fmt.Errorf("invalid CreateClusterOptions: ComputeNodeCount must be greater than 0. Got %d", options.ComputeNodeCount)
	}

	if options.MultiAZ {
		if options.ComputeNodeCount > 0 && math.Mod(float64(options.ComputeNodeCount), float64(3)) != 0 {
			return options, fmt.Errorf("invalid CreateClusterOptions: MultiAZ requires ComputeNodeCount to be divisible by 3. Got %d", options.ComputeNodeCount)
		}
	}

	if options.CCS {
		switch options.CloudProvider {
		case CloudProviderAWS:
			if options.CreateAWSClusterOptions == nil {
				return options, errors.New("invalid CreateClusterOptions: CreateAWSClusterOptions must be set for AWS CCS clusters")
			}
			if options.CreateAWSClusterOptions.AccountID == "" || options.CreateAWSClusterOptions.AccessKeyID == "" || options.CreateAWSClusterOptions.SecretAccessKey == "" {
				return options, errors.New("invalid CreateClusterOptions: AccountID, AccessKeyID, and SecretAccessKey must be set for AWS CCS clusters")
			}
		case CloudProviderGCP:
			if options.CreateGCPClusterOptions == nil {
				return options, errors.New("invalid CreateClusterOptions: CreateGCPClusterOptions must be set for GCP CCS clusters")
			}
		}
	}

	return options, nil
}
