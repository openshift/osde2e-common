# This is the terraform integration Fedramp in using in their SoP.
# Found inside the gitlab boundary ./service/fedramp-ops-sop/-/blob/main/onboarding_guides/create_a_rosa_devtest_cluster.md
provider "aws" {
  region =  var.aws_region
  profile = "default"
  default_tags {
    tags = {
      owner = "terraform"
    }
  }
}

variable "aws_region" {
  type        = string
  description = "The region to create the ROSA cluster in"

  validation {
    condition     = contains(["eu-central-1", "eu-west-1", "us-east-1", "us-east-2", "us-west-2", "us-gov-west-1", us-gov-east-1], var.aws_region)
    error_message = "HyperShift is currently only available in these regions: eu-central-1, eu-west-1, us-east-1, us-east-2, us-west-2, FedRamp is only available in us-gov-west-1 and us-gov-east-1."
  }
}

module "rosa_sts_privatelink_networking" {
  source = "github.com/mjlshen/rosa-sts-privatelink"
  name     = "rosa-sts-pl"
  cidr     = "10.0.0.0/16"
  multi_az = true
}

output "private_subnet_ids" {
  value = module.rosa_sts_privatelink_networking.private_subnet_ids
}