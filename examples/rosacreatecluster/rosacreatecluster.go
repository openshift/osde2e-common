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

		clusterChannel = "candidate"
		clusterName    = "cluster-123"
		clusterVersion = "4.12.20"

		hostedCP = false
		sts      = true

		logger = ginkgo.GinkgoLogr
	)

	provider, err := rosa.New(
		ctx,
		os.Getenv("OCM_TOKEN"),
		os.Getenv("OCM_CLIENT_ID"),
		os.Getenv("OCM_CLIENT_SECRET"),
		os.Getenv("HTTPS_PROXY"),
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

	clusterID, err := provider.CreateCluster(
		ctx,
		&rosa.CreateClusterOptions{
			ClusterName:  clusterName,
			ChannelGroup: clusterChannel,
			HostedCP:     hostedCP,
			STS:          sts,
			Version:      clusterVersion,
		},
	)
	if err != nil {
		log.Fatalf("Failed to create rosa cluster: %v", err)
	}

	logger.Info("Cluster created!", "clusterID", clusterID)
}
