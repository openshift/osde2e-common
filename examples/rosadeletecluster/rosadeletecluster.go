package main

import (
	"context"
	"log"
	"os"

	"github.com/onsi/ginkgo/v2"
	ocmclient "github.com/openshift/osde2e-common/pkg/clients/ocm"
	awscloud "github.com/openshift/osde2e-common/pkg/clouds/aws"
	"github.com/openshift/osde2e-common/pkg/openshift/rosa"
)

func main() {
	var (
		ctx = context.Background()

		clusterName = "cluster-123"
		clusterID   = "cluster-123-id"

		hostedCP = false
		sts      = true

		logger = ginkgo.GinkgoLogr
	)

	provider, err := rosa.New(
		ctx,
		os.Getenv("OCM_TOKEN"),
		ocmclient.Stage,
		logger,
		&awscloud.AWSCredentials{Profile: "", Region: ""},
	)
	if err != nil {
		log.Fatalf("Failed to create rosa provider: %v", err)
	}

	defer func() {
		_ = provider.Client.Close()
	}()

	err = provider.DeleteCluster(
		ctx,
		&rosa.DeleteClusterOptions{
			ClusterName: clusterName,
			ClusterID:   clusterID,
			HostedCP:    hostedCP,
			STS:         sts,
		},
	)
	if err != nil {
		log.Fatalf("Failed to delete rosa cluster: %v", err)
	}

	logger.Info("Cluster deleted!", "clusterID", clusterID)
}
