package rosa

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	"github.com/openshift/osde2e-common/internal/cmd"
	ocmclient "github.com/openshift/osde2e-common/pkg/clients/ocm"
	awscloud "github.com/openshift/osde2e-common/pkg/clouds/aws"
)

const (
	downloadURL    = "https://mirror.openshift.com/pub/openshift-v4/clients/rosa"
	minimumVersion = "1.2.24"
	tarFilename    = "rosa.tar.gz"
)

// Provider is a rosa provider
type Provider struct {
	*ocmclient.Client
	awsCredentials *awscloud.AWSCredentials
	ocmEnvironment ocmclient.Environment
	log            logr.Logger

	AWSRegion  string
	rosaBinary string
}

// providerError represents the provider custom error
type providerError struct {
	err error
}

// Error returns the formatted error message when providerError is invoked
func (r *providerError) Error() string {
	return fmt.Sprintf("failed to construct rosa provider: %v", r.err)
}

// RunCommand runs the rosa command provided
func (r *Provider) RunCommand(ctx context.Context, command *exec.Cmd) (io.Writer, io.Writer, error) {
	command.Env = append(command.Environ(), r.awsCredentials.CredentialsAsList()...)
	command.Env = append(command.Env, fmt.Sprintf("OCM_CONFIG=%s/ocm.json", os.TempDir()))
	commandWithArgs := fmt.Sprintf("rosa%s", strings.Split(command.String(), "rosa")[1])
	r.log.Info("Command", rosaCommandLoggerKey, commandWithArgs)
	return cmd.Run(command)
}

// Uninstall removes the rosa cli that was downloaded to the systems temp directory
func (r *Provider) Uninstall(ctx context.Context) error {
	if strings.Contains(r.rosaBinary, os.TempDir()) {
		return os.Remove(r.rosaBinary)
	}
	return nil
}

// cliExist checks if rosa cli is available else it will download it
func cliCheck() (string, error) {
	var (
		url          = fmt.Sprintf("%s/%s", downloadURL, minimumVersion)
		rosaFilename = fmt.Sprintf("%s/rosa", os.TempDir())
	)

	defer func() {
		_ = os.Remove(tarFilename)
	}()

	runtimeOS := runtime.GOOS
	switch runtimeOS {
	case "linux":
		url = fmt.Sprintf("%s/rosa-linux.tar.gz", url)
	case "darwin":
		url = fmt.Sprintf("%s/rosa-macosx.tar.gz", url)
	default:
		return "", fmt.Errorf("operating system %q is not supported", runtimeOS)
	}

	path, err := exec.LookPath("rosa")
	if path != "" && err == nil {
		return path, nil
	}

	response, err := http.Get(url)
	if err != nil || response.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("failed to download %s: %v", url, err)
	}
	defer response.Body.Close()

	tarFile, err := os.Create(tarFilename)
	if err != nil {
		return "", fmt.Errorf("failed to create %s tar file: %v", tarFilename, err)
	}
	defer tarFile.Close()

	rosaFile, err := os.Create(rosaFilename)
	if err != nil {
		return "", fmt.Errorf("failed to create %s tar file: %v", rosaFilename, err)
	}

	err = os.Chmod(rosaFilename, 0o755)
	if err != nil {
		return "", fmt.Errorf("failed to set file permissions to 0755 for %s: %v", rosaFilename, err)
	}

	defer rosaFile.Close()

	_, err = io.Copy(tarFile, response.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write content to %s: %v", tarFilename, err)
	}

	tarFileReader, err := os.Open(tarFilename)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %v", tarFilename, err)
	}
	defer tarFileReader.Close()

	gzipReader, err := gzip.NewReader(tarFileReader)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader for %s: %v", tarFilename, err)
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)

	for {
		_, err := tarReader.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			break
		}
		_, err = io.Copy(rosaFile, tarReader)
		if err != nil {
			break
		}
	}

	return rosaFilename, nil
}

// versionCheck verifies the rosa cli version meets the minimal version required
func versionCheck(ctx context.Context, rosaBinary string) (string, error) {
	stdout, _, err := cmd.Run(exec.CommandContext(ctx, rosaBinary, "version"))
	if err != nil {
		return "", err
	}

	versionSlice := strings.SplitAfter(fmt.Sprint(stdout), "\n")
	if len(versionSlice) == 0 {
		return "", fmt.Errorf("versionCheck failed to get version from cli standard out")
	}

	currentVersion, err := semver.NewVersion(strings.ReplaceAll(versionSlice[0], "\n", ""))
	if err != nil {
		return "", fmt.Errorf("versionCheck failed to parse version to semantic version: %v", err)
	}

	minVersion, err := semver.NewVersion(minimumVersion)
	if err != nil {
		return "", fmt.Errorf("versionCheck failed to parse minimum version to semantic version: %v", err)
	}

	if minVersion.Compare(currentVersion) == 1 {
		return "", fmt.Errorf("current rosa version is %q and must be >= %q", currentVersion.String(), minVersion)
	}

	return currentVersion.String(), nil
}

// verifyLogin validates the authentication details provided are valid by logging in with rosa cli
func verifyLogin(ctx context.Context, rosaBinary string, token string, ocmEnvironment ocmclient.Environment, awsCredentials *awscloud.AWSCredentials) error {
	commandArgs := []string{
		"login",
		"--token", token,
		"--env", string(ocmEnvironment),
	}

	command := exec.CommandContext(ctx, rosaBinary, commandArgs...)
	command.Env = append(command.Environ(), awsCredentials.CredentialsAsList()...)
	command.Env = append(command.Env, fmt.Sprintf("OCM_CONFIG=%s/ocm.json", os.TempDir()))

	_, _, err := cmd.Run(command)
	if err != nil {
		return fmt.Errorf("login failed %v", err)
	}
	return nil
}

// New handles constructing the rosa provider which creates a connection
// to openshift cluster manager "ocm". It is the callers responsibility
// to close the ocm connection when they are finished (defer provider.Connection.Close())
func New(ctx context.Context, token string, ocmEnvironment ocmclient.Environment, logger logr.Logger, args ...*awscloud.AWSCredentials) (*Provider, error) {
	if ocmEnvironment == "" || token == "" {
		return nil, &providerError{err: errors.New("some parameters are undefined, unable to construct osd provider")}
	}

	rosaBinary, err := cliCheck()
	if err != nil {
		return nil, &providerError{err: err}
	}

	version, err := versionCheck(ctx, rosaBinary)
	if err != nil {
		return nil, &providerError{err: err}
	}

	logger.Info("ROSA version", "version", version)

	awsCredentials := &awscloud.AWSCredentials{}
	if len(args) == 1 {
		awsCredentials = args[0]
	}

	err = awsCredentials.Set()
	if err != nil {
		return nil, &providerError{err: fmt.Errorf("aws credential set and validation failed: %v", err)}
	}

	err = verifyLogin(ctx, rosaBinary, token, ocmEnvironment, awsCredentials)
	if err != nil {
		return nil, &providerError{err: err}
	}

	provider := &Provider{
		awsCredentials: awsCredentials,
		ocmEnvironment: ocmEnvironment,
		rosaBinary:     rosaBinary,
		Client:         nil,
		log:            logger,
	}

	if awsCredentials.Region == "random" {
		// Set a temporary region to select a random region later on
		awsCredentials.Region = "us-east-1"
		awsCredentials.Region, err = provider.selectRandomRegion(ctx)
		if err != nil {
			return nil, &providerError{err: err}
		}
	}

	provider.AWSRegion = awsCredentials.Region

	provider.Client, err = ocmclient.New(ctx, token, ocmEnvironment)
	if err != nil {
		return nil, &providerError{err: err}
	}

	return provider, nil
}
