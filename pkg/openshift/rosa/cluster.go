package rosa

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	clustersmgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/osde2e-common/internal/cmd"
	"github.com/openshift/osde2e-common/pkg/clients/ocm"
	openshiftclient "github.com/openshift/osde2e-common/pkg/clients/openshift"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

const defaultAccountRolesPrefix = "ManagedOpenShift"

// CreateClusterOptions represents data used to create clusters
type CreateClusterOptions struct {
	FIPS                         bool
	HostedCP                     bool
	MultiAZ                      bool
	STS                          bool
	MintMode                     bool
	SkipHealthCheck              bool
	UseDefaultAccountRolesPrefix bool
	EnableAutoscaling            bool
	ETCDEncryption               bool

	HostPrefix  int
	Replicas    int
	MinReplicas int
	MaxReplicas int

	ArtifactDir               string
	AdditionalTrustBundleFile string
	ChannelGroup              string
	ClusterName               string
	ComputeMachineType        string
	HTTPProxy                 string
	HTTPSProxy                string
	MachineCidr               string
	Mode                      string
	NetworkType               string
	OidcConfigID              string
	PodCIDR                   string
	ServiceCIDR               string
	SubnetIDs                 string
	Version                   string
	WorkingDir                string

	accountRoles accountRoles

	Properties map[string]string

	InstallTimeout     time.Duration
	HealthCheckTimeout time.Duration
	ExpirationDuration time.Duration
}

// DeleteClusterOptions represents data used to delete clusters
type DeleteClusterOptions struct {
	ArtifactDir string
	ClusterName string
	WorkingDir  string

	oidcConfigID string

	DeleteHostedCPVPC  bool
	DeleteOidcConfigID bool
	HostedCP           bool
	STS                bool
	MintMode           bool

	UninstallTimeout time.Duration
}

// clusterError represents the custom error
type clusterError struct {
	action string
	err    error
}

// Error returns the formatted error message when clusterError is invoked
func (c *clusterError) Error() string {
	return fmt.Sprintf("%s cluster failed: %v", c.action, c.err)
}

// CreateCluster creates a rosa cluster using the provided inputs
func (r *Provider) CreateCluster(ctx context.Context, options *CreateClusterOptions) (string, error) {
	const action = "create"

	options.setDefaultCreateClusterOptions()

	if options.ChannelGroup == "nightly" {
		// TODO: validate version is as expected
		r.log.Info("Waiting up to 5 minutes for nightly version to be available", "version", options.Version)
		if err := wait.For(func(ctx context.Context) (bool, error) {
			versions, err := r.Versions(ctx, options.ChannelGroup, options.HostedCP)
			if err != nil {
				return false, err
			}
			for _, version := range versions {
				if strings.Contains(version.ID, options.Version) {
					r.log.Info("Nightly version found in the nightly channel-group", "version", options.Version)
					return true, nil
				}
			}
			return false, nil
		}); err != nil {
			return "", &clusterError{action: action, err: err}
		}
	}

	err := r.regionCheck(ctx, r.awsCredentials.Region, options.HostedCP, options.MultiAZ)
	if err != nil {
		return "", &clusterError{action: action, err: err}
	}

	if options.HostedCP || options.STS {
		version, err := semver.NewVersion(options.Version)
		if err != nil {
			return "", &clusterError{action: action, err: fmt.Errorf("failed to parse version (%q) into semantic version: %v", options.Version, err)}
		}
		majorMinor := fmt.Sprintf("%d.%d", version.Major(), version.Minor())

		accountRolesPrefix := options.ClusterName
		if options.UseDefaultAccountRolesPrefix {
			accountRolesPrefix = fmt.Sprintf("%s-%s", defaultAccountRolesPrefix, majorMinor)
		}

		accountRoles, err := r.createAccountRoles(ctx, accountRolesPrefix, majorMinor, options.ChannelGroup)
		if err != nil {
			return "", &clusterError{action: action, err: err}
		}
		options.accountRoles = *accountRoles

		if options.OidcConfigID == "" {
			options.OidcConfigID, err = r.createOIDCConfig(
				ctx,
				options.ClusterName,
				options.accountRoles.installerRoleARN,
			)
			if err != nil {
				return "", &clusterError{action: action, err: err}
			}
		}
	}

	if options.HostedCP {
		if options.SubnetIDs == "" {
			vpc, err := r.createHostedControlPlaneVPC(
				ctx,
				options.ClusterName,
				r.awsCredentials.Region,
				options.WorkingDir,
			)
			if err != nil {
				return "", &clusterError{action: action, err: err}
			}
			options.SubnetIDs = fmt.Sprintf("%s,%s", vpc.privateSubnet, vpc.publicSubnet)
		}
	}

	clusterID, err := r.createCluster(ctx, options)
	if err != nil {
		return "", &clusterError{action: action, err: err}
	}

	err = r.waitForClusterToBeInstalled(ctx, clusterID, options.ClusterName, options.ArtifactDir, options.InstallTimeout)
	if err != nil {
		return clusterID, &clusterError{action: action, err: err}
	}

	if !options.SkipHealthCheck {
		kubeconfigFile, err := r.Client.KubeconfigFile(ctx, clusterID, os.TempDir())
		if err != nil {
			return clusterID, &clusterError{action: action, err: err}
		}

		client, err := openshiftclient.NewFromKubeconfig(kubeconfigFile, r.log)
		if err != nil {
			return clusterID, &clusterError{action: action, err: err}
		}

		err = r.waitForClusterToBeHealthy(
			ctx,
			client,
			options.ClusterName,
			options.ArtifactDir,
			options.HostedCP,
			options.HealthCheckTimeout,
		)
		if err != nil {
			return clusterID, &clusterError{action: action, err: err}
		}
	}

	return clusterID, nil
}

// DeleteCluster deletes a rosa cluster using the provided inputs
func (r *Provider) DeleteCluster(ctx context.Context, options *DeleteClusterOptions) error {
	const action = "delete"

	options.setDefaultDeleteClusterOptions()

	cluster, err := r.findCluster(ctx, options.ClusterName)
	if err != nil {
		return &clusterError{action: action, err: fmt.Errorf("failed to locate cluster in ocm environment: %s: %s", r.ocmEnvironment, err)}
	}

	if options.HostedCP {
		options.oidcConfigID = cluster.AWS().STS().OidcConfig().ID()
	}

	err = r.deleteCluster(ctx, cluster.ID())
	if err != nil {
		return &clusterError{action: action, err: err}
	}

	err = r.waitForClusterToBeDeleted(ctx, cluster.Name(), options.ArtifactDir, options.UninstallTimeout)
	if err != nil {
		return &clusterError{action: action, err: err}
	}

	if options.STS {
		operatorRolePrefix := cluster.AWS().STS().OperatorRolePrefix()
		err = r.deleteOperatorRoles(ctx, cluster.ID(), operatorRolePrefix, options.oidcConfigID)
		if err != nil {
			return &clusterError{action: action, err: err}
		}

		err = r.deleteOIDCConfigProvider(ctx, cluster.ID(), options.oidcConfigID)
		if err != nil {
			return &clusterError{action: action, err: err}
		}
	}

	if options.HostedCP {
		if options.DeleteOidcConfigID {
			err := r.deleteOIDCConfig(ctx, options.oidcConfigID)
			if err != nil {
				return &clusterError{action: action, err: err}
			}
		}

		if options.DeleteHostedCPVPC {
			err = r.deleteHostedControlPlaneVPC(
				ctx,
				cluster.Name(),
				r.awsCredentials.Region,
				options.WorkingDir,
			)
			if err != nil {
				return &clusterError{action: action, err: err}
			}
		}
	}

	if options.STS {
		if !strings.Contains(cluster.AWS().STS().RoleARN(), defaultAccountRolesPrefix) {
			err = r.deleteAccountRoles(ctx, options.ClusterName)
			if err != nil {
				return &clusterError{action: action, err: err}
			}
		}
	}

	return nil
}

// validateCreateClusterOptions verifies required options are set and sets defaults if undefined
func (r *Provider) validateCreateClusterOptions(options *CreateClusterOptions) (*CreateClusterOptions, error) {
	var errs []error

	if options.ClusterName == "" {
		errs = append(errs, errors.New("cluster name is required"))
	}

	if options.ChannelGroup == "" {
		options.ChannelGroup = "stable"
	}

	if options.ComputeMachineType == "" {
		options.ComputeMachineType = "m5.xlarge"
	}

	if options.MachineCidr == "" {
		options.MachineCidr = "10.0.0.0/16"
	}

	if options.Version == "" {
		errs = append(errs, errors.New("cluster version is required"))
	}

	if options.Replicas == 0 {
		options.Replicas = 2
	}

	if options.HostedCP {
		if options.OidcConfigID == "" {
			errs = append(errs, errors.New("oidc config id is required for hosted control plane clusters"))
		}

		if options.SubnetIDs == "" {
			errs = append(errs, errors.New("subnet ids is required for hosted control plane clusters"))
		}
	}

	if options.HostedCP || options.STS {
		if options.accountRoles.controlPlaneRoleARN == "" {
			errs = append(errs, errors.New("iam role arn for control plane is required"))
		}

		if options.accountRoles.installerRoleARN == "" {
			errs = append(errs, errors.New("iam role arn for installer is required"))
		}

		if options.accountRoles.supportRoleARN == "" {
			errs = append(errs, errors.New("iam role arn for support role is required"))
		}

		if options.accountRoles.workerRoleARN == "" {
			errs = append(errs, errors.New("iam role for worker role is required"))
		}
	}

	if len(errs) != 0 {
		for _, err := range errs {
			r.log.Error(err, "create cluster option undefined")
		}
		return options, errors.New("one or more create cluster options are missing")
	}

	return options, nil
}

// createCluster handles sending the request to create the cluster
func (r *Provider) createCluster(ctx context.Context, options *CreateClusterOptions) (string, error) {
	options, err := r.validateCreateClusterOptions(options)
	if err != nil {
		return "", fmt.Errorf("cluster options validation failed: %v", err)
	}

	commandArgs := []string{
		"create", "cluster",
		"--output", "json",
		"--cluster-name", options.ClusterName,
		"--channel-group", options.ChannelGroup,
		"--compute-machine-type", options.ComputeMachineType,
		"--machine-cidr", options.MachineCidr,
		"--region", r.awsCredentials.Region,
		"--version", options.Version,
		"--host-prefix", fmt.Sprint(options.HostPrefix),
		"--oidc-config-id", options.OidcConfigID,
		"--yes",
	}

	if !options.HostedCP {
		commandArgs = append(commandArgs, []string{
			"--role-arn", options.accountRoles.installerRoleARN,
			"--controlplane-iam-role", options.accountRoles.controlPlaneRoleARN,
			"--support-role-arn", options.accountRoles.supportRoleARN,
			"--worker-iam-role", options.accountRoles.workerRoleARN,
		}...)
	}

	if options.PodCIDR != "" {
		commandArgs = append(commandArgs, "--pod-cidr", options.PodCIDR)
	}

	if options.ServiceCIDR != "" {
		commandArgs = append(commandArgs, "--service-cidr", options.ServiceCIDR)
	}

	if len(options.Properties) > 0 {
		for key, value := range options.Properties {
			commandArgs = append(commandArgs, "--properties", fmt.Sprintf("%s:%s", key, value))
		}
	}

	if options.HostedCP || options.STS {
		commandArgs = append(commandArgs, "--mode", "auto")
	}

	if options.HostedCP {
		commandArgs = append(commandArgs, "--hosted-cp")
		commandArgs = append(commandArgs, "--oidc-config-id", options.OidcConfigID)
		commandArgs = append(commandArgs, []string{
			"--role-arn", options.accountRoles.hcpInstallerRoleARN,
			"--support-role-arn", options.accountRoles.hcpSupportRoleARN,
			"--worker-iam-role", options.accountRoles.hcpWorkerRoleARN,
		}...)
	}

	if options.SubnetIDs != "" {
		commandArgs = append(commandArgs, "--subnet-ids", options.SubnetIDs)
	}

	if options.STS {
		commandArgs = append(commandArgs, "--sts")
	}

	if options.MintMode {
		commandArgs = append(commandArgs, "--mint-mode")
	}

	if options.FIPS {
		commandArgs = append(commandArgs, "--fips")
	}

	if options.NetworkType != "" && options.NetworkType != "OVNKubernetes" {
		commandArgs = append(commandArgs, "--network-type", options.NetworkType)
	}

	if options.MultiAZ {
		commandArgs = append(commandArgs, "--multi-az")

		if options.Replicas < 3 {
			options.Replicas = 3
		}
	}

	if options.EnableAutoscaling {
		commandArgs = append(commandArgs, "--enable-autoscaling")
	}

	if options.ETCDEncryption {
		commandArgs = append(commandArgs, "--etcd-encryption")
	}

	if options.MinReplicas > 0 {
		commandArgs = append(commandArgs, "--min-replicas", fmt.Sprint(options.MinReplicas))
	}

	if options.MaxReplicas > 0 {
		commandArgs = append(commandArgs, "--max-replicas", fmt.Sprint(options.MaxReplicas))
	}

	if options.MinReplicas == 0 && options.MaxReplicas == 0 {
		commandArgs = append(commandArgs, "--replicas", fmt.Sprint(options.Replicas))
	}

	if options.SubnetIDs != "" {
		if options.HTTPProxy != "" {
			commandArgs = append(commandArgs, "--http-proxy", options.HTTPProxy)
		}

		if options.HTTPSProxy != "" {
			commandArgs = append(commandArgs, "--https-proxy", options.HTTPSProxy)
		}

		if options.AdditionalTrustBundleFile != "" {
			commandArgs = append(commandArgs, "--additional-trust-bundle-file", options.AdditionalTrustBundleFile)
		}
	}

	// add expiration if provided, expiration can not be modified on prod, skip
	if options.ExpirationDuration > 0 && r.ocmEnvironment != ocm.Production {
		commandArgs = append(commandArgs, "--expiration-time", time.Now().Add(options.ExpirationDuration).UTC().Format(time.RFC3339))
	}

	r.log.Info("Initiating cluster creation", clusterNameLoggerKey, options.ClusterName, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	_, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return "", fmt.Errorf("error: %v, stderr: %v", err, stderr)
	}

	cluster, err := r.findCluster(ctx, options.ClusterName)
	if err != nil {
		return "", err
	}

	clusterID := cluster.ID()

	r.log.Info("Cluster creation initiated!", clusterNameLoggerKey, options.ClusterName,
		clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	return clusterID, err
}

// findCluster gets the cluster the body
func (r *Provider) findCluster(ctx context.Context, clusterName string) (*clustersmgmtv1.Cluster, error) {
	query := fmt.Sprintf("product.id = 'rosa' AND (name = '%[1]s' OR id = '%[1]s')", clusterName)
	response, err := r.ClustersMgmt().V1().Clusters().List().
		Search(query).
		Page(1).
		Size(1).
		SendContext(ctx)

	if response.Total() == 1 {
		return response.Items().Slice()[0], nil
	}
	return nil, fmt.Errorf("cluster %q not found in ocm %q: %v", clusterName, r.ocmEnvironment, err)
}

// deleteCluster handles sending the request to delete the cluster
func (r *Provider) deleteCluster(ctx context.Context, clusterID string) error {
	if clusterID == "" {
		return errors.New("cluster ID is undefined and is required")
	}

	r.log.Info("Initiating cluster deletion", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	commandArgs := []string{
		"delete", "cluster",
		"--cluster", clusterID,
		"--yes",
	}

	_, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return fmt.Errorf("error: %v, stderr: %v", err, stderr)
	}

	r.log.Info("Cluster deletion initiated!", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	return err
}

// waitForClusterToBeInstalled waits for the cluster to be in a ready state
func (r *Provider) waitForClusterToBeInstalled(ctx context.Context, clusterID, clusterName, reportDir string, timeout time.Duration) error {
	getClusterState := func() (string, error) {
		commandArgs := []string{
			"describe", "cluster",
			"--cluster", clusterID,
			"--output", "json",
		}

		stdout, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
		if err != nil {
			return "", fmt.Errorf("error: %v, stderr: %v", err, stderr)
		}

		output, err := cmd.ConvertOutputToMap(stdout)
		if err != nil {
			return "", fmt.Errorf("failed to convert output to map: %v", err)
		}

		clusterState := fmt.Sprint(output["status"].(map[string]any)["state"])

		return clusterState, err
	}

	r.log.Info("Waiting for cluster to be installed", clusterIDLoggerKey, clusterID, clusterNameLoggerKey, clusterName, timeoutLoggerKey, timeout.Round(time.Second).String(), ocmEnvironmentLoggerKey, r.ocmEnvironment)

	err := wait.For(func(ctx context.Context) (bool, error) {
		clusterState, err := getClusterState()
		if err != nil {
			return false, err
		}

		if clusterState != "ready" {
			r.log.Info("Cluster not in ready state", clusterIDLoggerKey, clusterID, clusterNameLoggerKey, clusterName, clusterStateLoggerKey, clusterState, ocmEnvironmentLoggerKey, r.ocmEnvironment)
			return false, nil
		}

		r.log.Info("Cluster is ready!", clusterIDLoggerKey, clusterID, clusterNameLoggerKey, clusterName, ocmEnvironmentLoggerKey, r.ocmEnvironment)
		return true, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(30*time.Second))
	if err != nil {
		return fmt.Errorf("cluster %q failed to enter ready state in the alloted time %q: %w", clusterID, timeout, err)
	}
	return nil
}

// waitForClusterToBeHealthy waits for the cluster health check job to succeed
func (r *Provider) waitForClusterToBeHealthy(ctx context.Context, client *openshiftclient.Client, clusterName, reportDir string, hostedCP bool, timeout time.Duration) error {
	if hostedCP {
		cluster, err := r.findCluster(ctx, clusterName)
		if err != nil {
			return fmt.Errorf("hosted control plane cluster pre health check tasks failed, unable to locate cluster: %v", err)
		}

		return client.HCPClusterHealthy(ctx, cluster.Nodes().Compute(), timeout)
	}
	return client.OSDClusterHealthy(ctx, "osd-cluster-ready", reportDir, timeout)
}

// waitForClusterToBeDeleted waits for the cluster to be deleted
func (r *Provider) waitForClusterToBeDeleted(ctx context.Context, clusterName, reportDir string, timeout time.Duration) error {
	defer func() {
		// TODO: Fix this, logs are unavailable once cluster is deleted
		err := r.clusterLog(ctx, "uninstall", clusterName, reportDir)
		r.log.Error(err, "failed to get cluster uninstall log", clusterNameLoggerKey, clusterName, ocmEnvironmentLoggerKey, r.ocmEnvironment)
	}()

	err := wait.For(func(ctx context.Context) (bool, error) {
		cluster, err := r.findCluster(ctx, clusterName)
		if err == nil && cluster != nil {
			r.log.Info("Cluster is uninstalling...", clusterNameLoggerKey, clusterName, clusterStateLoggerKey, cluster.State(), ocmEnvironmentLoggerKey, r.ocmEnvironment)
			return false, nil
		}

		r.log.Info("Cluster no longer exists!", clusterNameLoggerKey, clusterName, ocmEnvironmentLoggerKey, r.ocmEnvironment)
		return true, nil
	}, wait.WithTimeout(timeout), wait.WithInterval(30*time.Second))
	if err != nil {
		return fmt.Errorf("cluster %q failed to finish uninstalling in the alloted time", clusterName)
	}
	return nil
}

// setDefaultCreateClusterOptions sets default options when creating clusters
func (o *CreateClusterOptions) setDefaultCreateClusterOptions() {
	if o.HostedCP {
		o.STS = true
		o.setInstallTimeout(30)
		o.setHealthCheckTimeout(20)
	} else {
		o.setInstallTimeout(120)
		o.setHealthCheckTimeout(45)
	}

	if o.ArtifactDir == "" {
		o.ArtifactDir = os.TempDir()
	}

	if o.WorkingDir == "" {
		o.WorkingDir = os.TempDir()
	}
}

func (o *CreateClusterOptions) setInstallTimeout(duration time.Duration) {
	if o.InstallTimeout == 0 {
		o.InstallTimeout = duration * time.Minute
	}
}

func (o *CreateClusterOptions) setHealthCheckTimeout(duration time.Duration) {
	if o.HealthCheckTimeout == 0 {
		o.HealthCheckTimeout = duration * time.Minute
	}
}

// setDefaultDeleteClusterOptions sets default options when creating clusters
func (o *DeleteClusterOptions) setDefaultDeleteClusterOptions() {
	if o.HostedCP {
		o.STS = true
		o.DeleteHostedCPVPC = true
	}

	if o.ArtifactDir == "" {
		o.ArtifactDir = os.TempDir()
	}

	if o.WorkingDir == "" {
		o.WorkingDir = os.TempDir()
	}

	if o.UninstallTimeout == 0 {
		o.UninstallTimeout = 30 * time.Minute
	}
}
