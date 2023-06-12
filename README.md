# OSDe2e Common

[![GoDoc](https://godoc.org/github.com/openshift/osde2e-common?status.svg)](https://godoc.org/github.com/openshift/osde2e-common)

## Overview

OSDe2e common is a go module that provides common modules that can be used
when working with Managed OpenShift (e.g. OSD, ROSA, ROSA HCP).

This module provides a variety of helpers that eliminate the need to
create/duplicate code between go modules. Below are a few examples of what
the module provides for its consumers:

* OCM (OpenShift Cluster Manager) client
* OpenShift client (based on [e2e-framework](https://github.com/kubernetes-sigs/e2e-framework))
* Prometheus client

## Audience

As the [overview](#overview) section mentioned, osde2e common module can be
consumed by any audience. Below are some of the current consumers:

* [OSDe2e](https://github.com/openshift/osde2e)
* [OSD SREP Operators](https://github.com/openshift?q=operator&type=public&language=go&sort=)

## Questions

Have questions or issues about osde2e-common module? Open an
[issue](https://github.com/openshift/osde2e-common/issues/new) and one
of the project maintainers will respond.
