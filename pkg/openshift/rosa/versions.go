package rosa

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/openshift/osde2e-common/internal/cmd"
)

// versionError represents the custom error
type versionError struct {
	action string
	err    error
}

// Error returns the formatted error message when versionError is invoked
func (v *versionError) Error() string {
	return fmt.Sprintf("%s versions failed: %s", v.action, v.err)
}

// version represents a rosa version object
type version struct {
	ID                        string    `json:"id"`
	Href                      string    `json:"href"`
	AvailableUpgrades         []string  `json:"available_upgrades"`
	ChannelGroup              string    `json:"channel_group"`
	EndOfLifeTimestamp        time.Time `json:"end_of_life_timestamp"`
	RawID                     string    `json:"raw_id"`
	ReleaseImage              string    `json:"release_image"`
	RosaEnabled               bool      `json:"rosa_enabled"`
	Default                   bool      `json:"default"`
	Enabled                   bool      `json:"enabled"`
	HostedControlPlaneEnabled bool      `json:"hosted_control_plane_enabled"`
}

// Versions returns the rosa versions for the supported channel and additional options provided
func (r *Provider) Versions(ctx context.Context, channelGroup string, hostedCP bool) ([]*version, error) {
	const action = "get"

	commandArgs := []string{
		"list", "versions",
		"--channel-group", channelGroup,
		"--output", "json",
	}

	if hostedCP {
		commandArgs = append(commandArgs, "--hosted-cp")
	}

	r.log.Info("Getting rosa versions", clusterChannelGroupLoggerKey, channelGroup,
		"hostedCP", hostedCP, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	stdout, stderr, err := r.RunCommand(ctx, exec.CommandContext(ctx, r.rosaBinary, commandArgs...))
	if err != nil {
		return nil, &versionError{action: action, err: fmt.Errorf("error: %v, stderr: %v", err, stderr)}
	}

	availableVersions, err := cmd.ConvertOutputToListOfMaps(stdout)
	if err != nil {
		return nil, &versionError{action: action, err: fmt.Errorf("failed to convert output to list of maps: %v", err)}
	}

	var versions []*version

	availableVersionsBytes, err := json.Marshal(availableVersions)
	if err != nil {
		return nil, &versionError{action: action, err: fmt.Errorf("failed to marshal version data: %v", err)}
	}

	err = json.Unmarshal(availableVersionsBytes, &versions)
	if err != nil {
		return nil, &versionError{action: action, err: fmt.Errorf("failed to unmarshal version data: %v", err)}
	}

	r.log.Info("ROSA versions retrieved!", clusterChannelGroupLoggerKey, channelGroup,
		"hostedCP", hostedCP, ocmEnvironmentLoggerKey, r.ocmEnvironment)

	return versions, nil
}
