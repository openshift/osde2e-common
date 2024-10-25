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
	/*
		If you have all the vars to create it one-shot
		If using multi-az add "--multi-az" flag
		rosa create cluster --cluster-name $ROSA_CLUSTER_NAME \
		  --region $AWS_DEFAULT_REGION \
		  --version $VERSION \
		  --subnet-ids=$PRIVATE_SUBNET \ # Terraform output
		  --machine-cidr=10.0.0.0/16 \ # Added as part of the private link Flag
		  --private-link \
		  --sts
	*/
	var (
		ctx = context.Background()

		clusterChannel = "stable"
		clusterName    = "frcluster-124"
		clusterVersion = "4.12.60"

		privatelink = true
		sts         = true

		logger = ginkgo.GinkgoLogr
	)

	provider, err := rosa.New(
		ctx,
		os.Getenv("OCM_TOKEN"),
		os.Getenv("OCM_CLIENT_ID"),
		os.Getenv("OCM_CLIENT_SECRET"),
		os.Getenv("DEBUG"),
		ocmclient.FedRampIntegration,
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
			PrivateLink:  privatelink,
			STS:          sts,
			Version:      clusterVersion,
		},
	)
	if err != nil {
		log.Fatalf("Failed to create rosa cluster: %v", err)
	}

	logger.Info("Cluster created!", "clusterID", clusterID)
}
