package rosa

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/openshift/osde2e-common/internal/cmd"
)

const (
	fedRampRolesCount    = 4 // HCP roles are disabled for FedRamp
	commercialRolesCount = 7
)

// accountRoles represents all roles for a given prefix/version
type accountRoles struct {
	controlPlaneRoleARN string
	installerRoleARN    string
	supportRoleARN      string
	workerRoleARN       string
	hcpInstallerRoleARN string
	hcpSupportRoleARN   string
	hcpWorkerRoleARN    string
}

// accountRolesError represents the custom error
type accountRolesError struct {
	action string
	err    error
}

// Error returns the formatted error message when accountRolesError is invoked
func (a *accountRolesError) Error() string {
	return fmt.Sprintf("%s account roles failed: %v", a.action, a.err)
}

// createAccountRoles creates the account roles to be used when creating rosa clusters
func (r *Provider) CreateAccountRoles(ctx context.Context, prefix, version, channelGroup string) (*accountRoles, error) {
	const action = "create"
	var (
		accountRoles *accountRoles
		err          error
	)

	r.log.Info("Checking whether account roles exist", prefixLoggerKey, prefix, versionLoggerKey, version,
		clusterChannelGroupLoggerKey, channelGroup, ocmEnvironmentLoggerKey, r.ocmEnvironment)
	if accountRoles, err = r.getAccountRoles(ctx, prefix, version); err != nil {
		return nil, &accountRolesError{action: action, err: err}
	}

	if accountRoles == nil {
		r.log.Info("Creating account roles", prefixLoggerKey, prefix, versionLoggerKey, version,
			clusterChannelGroupLoggerKey, channelGroup, ocmEnvironmentLoggerKey, r.ocmEnvironment)

		commandArgs := []string{
			"create", "account-roles",
			"--prefix", prefix,
			"--version", version,
			"--channel-group", channelGroup,
			"--mode", "auto",
			"--yes",
		}

		// TODO: Open an RFE to rosa to support --output option
		if _, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...)); err != nil {
			return nil, &accountRolesError{action: action, err: fmt.Errorf("error: %v, stderr: %v", err, stderr)}
		}

		if accountRoles, err = r.getAccountRoles(ctx, prefix, version); err != nil {
			return nil, &accountRolesError{action: action, err: fmt.Errorf("failed to get account roles post account roles creation: %v", err)}
		}

		r.log.Info("Account roles created!", prefixLoggerKey, prefix, versionLoggerKey, version,
			clusterChannelGroupLoggerKey, channelGroup, ocmEnvironmentLoggerKey, r.ocmEnvironment)

		return accountRoles, nil
	}

	r.log.Info("Account roles already exist", prefixLoggerKey, prefix, versionLoggerKey, version,
		clusterChannelGroupLoggerKey, channelGroup, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	return accountRoles, nil
}

// deleteAccountRoles deletes the account roles that were created to create rosa clusters
func (r *Provider) DeleteAccountRoles(ctx context.Context, prefix string) error {
	r.log.Info("Deleting account roles", prefixLoggerKey, prefix, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	commandArgs := []string{
		"delete", "account-roles",
		"--prefix", prefix,
		"--mode", "auto",
		"--yes",
	}

	_, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return &accountRolesError{action: "delete", err: fmt.Errorf("error: %v, stderr: %v", err, stderr)}
	}

	r.log.Info("Account roles deleted!", prefixLoggerKey, prefix, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	return nil
}

// getAccountRoles gets the account roles matching the provided prefix and version
func (r *Provider) getAccountRoles(ctx context.Context, prefix, version string) (*accountRoles, error) {
	var (
		accountRolesFound = 0
		roles             = &accountRoles{}
	)

	commandArgs := []string{
		"list", "account-roles",
		"--output", "json",
	}

	stdout, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return nil, fmt.Errorf("failed to get account roles: %v", fmt.Sprint(stderr))
	}

	availableAccountRoles, err := cmd.ConvertOutputToListOfMaps(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to convert output to list of maps: %v", err)
	}

	for _, accountRole := range availableAccountRoles {
		roleName := fmt.Sprint(accountRole["RoleName"])
		roleARN := fmt.Sprint(accountRole["RoleARN"])
		roleVersion := fmt.Sprint(accountRole["Version"])
		roleType := fmt.Sprint(accountRole["RoleType"])

		if !strings.HasPrefix(roleName, prefix) {
			continue
		}

		if version != roleVersion {
			continue
		}

		if strings.HasPrefix(roleName, "HCP-ROSA") {
			switch roleType {
			case "Installer", "Support", "Worker":
				accountRolesFound += 1
				switch roleType {
				case "Installer":
					roles.hcpInstallerRoleARN = roleARN
				case "Support":
					roles.hcpSupportRoleARN = roleARN
				case "Worker":
					roles.hcpWorkerRoleARN = roleARN
				}
			default:
				r.log.Info("Unknown role type", roleARN, roleType)
			}
		} else {
			switch roleType {
			case "Control plane", "Installer", "Support", "Worker":
				accountRolesFound += 1
				switch roleType {
				case "Control plane":
					roles.controlPlaneRoleARN = roleARN
				case "Installer":
					roles.installerRoleARN = roleARN
				case "Support":
					roles.supportRoleARN = roleARN
				case "Worker":
					roles.workerRoleARN = roleARN
				}
			default:
				r.log.Info("Unknown role type", roleARN, roleType)
			}
		}
	}

	switch {
	case accountRolesFound == 0:
		return nil, nil
	case r.fedRamp && accountRolesFound == fedRampRolesCount:
		// Rosa blocks the creation of HCP roles for FedRamp clusters
		return roles, nil
	case accountRolesFound != commercialRolesCount:
		return nil, fmt.Errorf("one or more prefixed %q account roles does not exist: %+v", prefix, roles)
	default:
		return roles, nil
	}
}
