package rosa

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"

	"github.com/openshift/osde2e-common/internal/cmd"
)

// regionError represents the custom error
type regionError struct {
	err error
}

// Error returns the formatted error message when regionError is invoked
func (r *regionError) Error() string {
	return fmt.Sprintf("region check failed: %v", r.err)
}

// regionCheck verifies the region provided supports either hosted control plane clusters
// or multi az clusters based on the cluster creation options
func (r *Provider) regionCheck(ctx context.Context, regionName string, hostedCP, multiAZ bool) error {
	var (
		err     error
		enabled bool

		regionFound = false
	)

	commandArgs := []string{
		"list", "regions",
		"--output", "json",
	}

	if hostedCP {
		commandArgs = append(commandArgs, "--hosted-cp")
	}

	if multiAZ {
		commandArgs = append(commandArgs, "--multi-az")
	}

	r.log.Info("Performing ROSA AWS region check", "region", regionName, "hostedCP", hostedCP, "multiAZ", multiAZ)

	stdout, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return &regionError{fmt.Errorf("error: %v, stderr: %v", err, stderr)}
	}

	availableRegions, err := cmd.ConvertOutputToListOfMaps(stdout)
	if err != nil {
		return &regionError{err: fmt.Errorf("failed to convert output to list of maps: %v", err)}
	}

	for _, region := range availableRegions {
		if enabled, err = strconv.ParseBool(fmt.Sprint(region["enabled"])); err != nil {
			return &regionError{fmt.Errorf("failed to convert region %q 'enabled' string key to boolean value", regionName)}
		}

		if fmt.Sprint(region["id"]) != regionName {
			continue
		}

		regionFound = true

		if !enabled {
			return &regionError{fmt.Errorf("region %q is not enabled", regionName)}
		}

		break
	}

	if !regionFound {
		return &regionError{fmt.Errorf("region %q is not enabled/valid for the aws account in use and "+
			"supports: hostedCP=%t, multiAZ=%t", regionName, hostedCP, multiAZ)}
	}

	r.log.Info("ROSA AWS region check passed", "region", regionName, "hostedCP", hostedCP, "multiAZ", multiAZ)

	return nil
}
