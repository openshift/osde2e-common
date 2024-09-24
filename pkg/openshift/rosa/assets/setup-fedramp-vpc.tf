# provider.tf
terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 4.0"
    }
  }
}

provider "aws" {
  region = var.aws_region
  default_tags {
    tags = {
      owner   = "terraform"
      cluster = var.cluster_name
    }
  }
}

# variables.tf
variable "cluster_name" {
  type        = string
  description = "ROSA cluster name"
}

variable "aws_region" {
  type        = string
  description = "The region to create the ROSA cluster in"

  validation {
    condition     = contains(["eu-central-1", "eu-west-1", "us-east-1", "us-east-2", "us-west-2", "us-gov-west-1", "us-gov-east-1"], var.aws_region)
    error_message = "HyperShift is currently only available in these regions: eu-central-1, eu-west-1, us-east-1, us-east-2, us-west-2, FedRamp is only available in us-gov-west-1 and us-gov-east-1."
  }
}

variable "cidr" {
  type        = string
  default     = "10.0.0.0/16"
  description = "ROSA cluster VPC CIDR"
}

variable "multi_az" {
  type        = bool
  default     = false
  description = "Setup a multi-AZ VPC for the cluster"
}

variable "create_elb_iam_role" {
  type        = bool
  default     = true
  description = "Create the elasticloadbalancing IAM service-linked role"
}

# locals.tf
data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = var.multi_az ? [
    data.aws_availability_zones.available.names[0],
    data.aws_availability_zones.available.names[1],
    data.aws_availability_zones.available.names[2],
  ] : [data.aws_availability_zones.available.names[0]]
}

# main.tf
data "aws_iam_role" "elb" {
  name = "AWSServiceRoleForElasticLoadBalancing"
}

resource "aws_iam_service_linked_role" "elb" {
  count            = var.create_elb_iam_role && length(data.aws_iam_role.elb) == 0 ? 1 : 0
  aws_service_name = "elasticloadbalancing.amazonaws.com"
}

resource "aws_vpc" "rosa" {
  cidr_block           = var.cidr
  enable_dns_support   = true
  enable_dns_hostnames = true

  tags = {
    Name = "${var.cluster_name}-vpc"
  }
}

resource "aws_subnet" "rosa_private" {
  for_each          = { for idx, az in local.azs : az => idx }
  availability_zone = each.key
  vpc_id            = aws_vpc.rosa.id
  cidr_block        = cidrsubnet(var.cidr, length(local.azs), each.value)

  tags = {
    Name                              = "${var.cluster_name}-private-${each.key}"
    "kubernetes.io/role/internal-elb" = ""
  }

  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_subnet" "rosa_public" {
  for_each          = { for idx, az in local.azs : az => idx }
  availability_zone = each.key
  vpc_id            = aws_vpc.rosa.id
  cidr_block        = cidrsubnet(var.cidr, length(local.azs), length(local.azs) + each.value)

  tags = {
    Name                     = "${var.cluster_name}-public-${each.key}"
    "kubernetes.io/role/elb" = ""
  }

  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_internet_gateway" "rosa" {
  tags = {
    Name = "${var.cluster_name}-igw"
  }
}

resource "aws_internet_gateway_attachment" "rosa" {
  internet_gateway_id = aws_internet_gateway.rosa.id
  vpc_id              = aws_vpc.rosa.id
}

resource "aws_route_table" "rosa_public" {
  vpc_id = aws_vpc.rosa.id

  tags = {
    Name = "${var.cluster_name}-public"
  }
}

resource "aws_route_table_association" "rosa_public" {
  for_each       = aws_subnet.rosa_public
  subnet_id      = each.value.id
  route_table_id = aws_route_table.rosa_public.id
}

resource "aws_route" "internet_egress" {
  route_table_id         = aws_route_table.rosa_public.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.rosa.id
}

resource "aws_eip" "nat_gw" {
  for_each = aws_subnet.rosa_public

  vpc = true

  tags = {
    Name = "${var.cluster_name}-eip-${each.key}"
  }

  // EIP may require IGW to exist prior to association.
  // https://registry.terraform.io/providers/hashicorp/aws/latest/docs/resources/eip
  depends_on = [aws_internet_gateway_attachment.rosa]
}

resource "aws_nat_gateway" "rosa" {
  for_each          = aws_subnet.rosa_public
  allocation_id     = aws_eip.nat_gw[each.key].allocation_id
  connectivity_type = "public"
  subnet_id         = each.value.id

  tags = {
    Name = "${var.cluster_name}-nat-${each.key}"
  }
}

resource "aws_route_table" "rosa_private" {
  for_each = aws_subnet.rosa_private
  vpc_id   = aws_vpc.rosa.id

  tags = {
    Name = "${var.cluster_name}-private-${each.key}"
  }
}

resource "aws_route_table_association" "rosa_private" {
  for_each       = aws_subnet.rosa_private
  subnet_id      = each.value.id
  route_table_id = aws_route_table.rosa_private[each.key].id
}

resource "aws_route" "nat_gateway" {
  for_each               = aws_subnet.rosa_private
  route_table_id         = aws_route_table.rosa_private[each.key].id
  destination_cidr_block = "0.0.0.0/0"
  nat_gateway_id         = aws_nat_gateway.rosa[each.key].id
}

# outputs.tf
output "cluster-private-subnet" {
  description = "Private subnet ID"
  value       = values(aws_subnet.rosa_private)[0].id
}