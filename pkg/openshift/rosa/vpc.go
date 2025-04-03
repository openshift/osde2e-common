package rosa

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/openshift/osde2e-common/internal/terraform"

	"github.com/hashicorp/terraform-exec/tfexec"
)

// vpc represents the details of an aws vpc
type vpc struct {
	privateSubnet     string
	publicSubnet      string
	nodePrivateSubnet string
}

// vpcError represents the custom error
type vpcError struct {
	action string
	err    error
}

// Error returns the formatted error message when vpcError is invoked
func (h *vpcError) Error() string {
	return fmt.Sprintf("%s vpc failed: %v", h.action, h.err)
}

// copyFile copies the srcFile provided to the destFile
func copyFile(srcFile, destFile string) error {
	srcReader, err := FS.Open(srcFile)
	if err != nil {
		return fmt.Errorf("error opening %s file: %w", srcFile, err)
	}
	defer func() {
		if err = srcReader.Close(); err != nil {
			panic(err)
		}
	}()

	destReader, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("error creating runtime %s file: %w", destFile, err)
	}
	defer func() {
		if err = destReader.Close(); err != nil {
			panic(err)
		}
	}()

	_, err = io.Copy(destReader, srcReader)
	if err != nil {
		return fmt.Errorf("error copying source file to destination file: %w", err)
	}

	return nil
}

// createVPC creates the aws vpc used for provisioning hosted control plane or private link clusters
func (r *Provider) createVPC(ctx context.Context, clusterName, awsRegion, workingDir string, hostedCP, privateLink bool) (*vpc, error) {
	action := "create"
	var vpc vpc
	var tfFile string

	if clusterName == "" || awsRegion == "" || workingDir == "" {
		return nil, &vpcError{action: action, err: errors.New("one or more parameters is empty")}
	}

	tf, err := terraform.New(ctx, workingDir)
	if err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to construct terraform runner: %v", err)}
	}

	if err = tf.SetEnvVars(r.awsCredentials.CredentialsAsMap()); err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to set terraform runner aws credentials (env vars): %v", err)}
	}

	defer func() {
		_ = tf.Uninstall(ctx)
	}()

	r.log.Info("Creating aws vpc", clusterNameLoggerKey, clusterName, awsRegionLoggerKey, awsRegion)

	switch {
	case hostedCP:
		tfFile = "assets/setup-hcp-vpc.tf"
	case privateLink:
		tfFile = "assets/setup-fedramp-vpc.tf"
	default:
		return nil, &vpcError{action: action, err: fmt.Errorf("unsupported cluster flavor, hostedCP: %t, privateLink: %t", hostedCP, privateLink)}
	}

	if err := copyFile(tfFile, fmt.Sprintf("%s/setup-vpc.tf", workingDir)); err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to copy terraform file to working directory: %v", err)}
	}

	err = tf.Init(ctx)
	if err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to perform terraform init: %v", err)}
	}

	err = tf.Plan(
		ctx,
		tfexec.Var(fmt.Sprintf("aws_region=%s", awsRegion)),
		tfexec.Var(fmt.Sprintf("cluster_name=%s", clusterName)),
	)
	if err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to perform terraform plan: %v", err)}
	}

	err = tf.Apply(ctx)
	if err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to perform terraform apply: %v", err)}
	}

	output, err := tf.Output(ctx)
	if err != nil {
		return nil, &vpcError{action: action, err: fmt.Errorf("failed to perform terraform output: %v", err)}
	}

	vpc.privateSubnet = strings.ReplaceAll(string(output["cluster-private-subnet"].Value), "\"", "")
	vpc.publicSubnet = strings.ReplaceAll(string(output["cluster-public-subnet"].Value), "\"", "")
	vpc.nodePrivateSubnet = strings.ReplaceAll(string(output["node-private-subnet"].Value), "\"", "")

	r.log.Info("AWS vpc created!", clusterNameLoggerKey, clusterName, terraformWorkingDirLoggerKey, workingDir)

	return &vpc, err
}

// deleteVPC deletes the aws vpc used for provisioning hosted control plane or private link clusters
func (r *Provider) deleteVPC(ctx context.Context, clusterName, awsRegion, workingDir string) error {
	const action = "delete"

	if clusterName == "" || awsRegion == "" || workingDir == "" {
		return &vpcError{action: action, err: errors.New("one or more parameters is empty")}
	}

	tf, err := terraform.New(ctx, workingDir)
	if err != nil {
		return &vpcError{action: action, err: fmt.Errorf("failed to construct terraform runner: %v", err)}
	}

	if err = tf.SetEnvVars(r.awsCredentials.CredentialsAsMap()); err != nil {
		return &vpcError{action: action, err: fmt.Errorf("failed to set terraform runner aws credentials (env vars): %v", err)}
	}

	defer func() {
		_ = tf.Uninstall(ctx)
	}()

	r.log.Info("Deleting aws vpc", clusterNameLoggerKey, clusterName, awsRegionLoggerKey, awsRegion, terraformWorkingDirLoggerKey, workingDir)

	err = tf.Init(ctx)
	if err != nil {
		return &vpcError{action: action, err: fmt.Errorf("failed to perform terraform init: %v", err)}
	}

	err = tf.Destroy(
		ctx,
		tfexec.Var(fmt.Sprintf("aws_region=%s", awsRegion)),
		tfexec.Var(fmt.Sprintf("cluster_name=%s", clusterName)),
	)
	if err != nil {
		return &vpcError{action: action, err: fmt.Errorf("failed to perform terraform destroy: %v", err)}
	}

	r.log.Info("AWS vpc deleted!", clusterNameLoggerKey, clusterName)

	return err
}
