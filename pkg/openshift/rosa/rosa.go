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

	"github.com/go-logr/logr"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/openshift/osde2e-common/internal/cmd"
	ocmclient "github.com/openshift/osde2e-common/pkg/clients/ocm"
	awscloud "github.com/openshift/osde2e-common/pkg/clouds/aws"
)

const (
	downloadURL = "https://mirror.openshift.com/pub/openshift-v4/clients/rosa"
)

// Provider is a rosa provider
type Provider struct {
	*ocmclient.Client
	awsCredentials *awscloud.AWSCredentials
	ocmEnvironment ocmclient.Environment
	log            logr.Logger

	AWSRegion  string
	rosaBinary string
	debug      string

	fedRamp bool
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
	// If r.ocmEnvironment is not fedramp, then set the OCM_CONFIG environment variable
	if !r.fedRamp {
		command.Env = append(command.Env, fmt.Sprintf("OCM_CONFIG=%s/ocm.json", os.TempDir()))
	}

	if r.debug == "true" {
		command.Args = append(command.Args, "--debug")
	}

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

// cliCheck checks if rosa cli is available else it will download it
func cliCheck() (string, error) {
	var (
		url             = fmt.Sprintf("%s/latest", downloadURL)
		rosaFilename    = fmt.Sprintf("%s/rosa", os.TempDir())
		rosaTarFilePath = fmt.Sprintf("%s/rosa.tar.gz", os.TempDir())
	)

	defer func() {
		_ = os.Remove(rosaTarFilePath)
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

	retryClient := retryablehttp.NewClient()
	retryClient.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		ok, e := retryablehttp.DefaultRetryPolicy(ctx, resp, err)
		if !ok && resp.StatusCode == http.StatusRequestTimeout {
			return true, nil
		}
		return ok, e
	}

	response, err := retryClient.Get(url)
	if err != nil || response.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("failed to download %s: %v", url, err)
	}
	defer response.Body.Close()

	tarFile, err := os.Create(rosaTarFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to create %s tar file: %v", rosaTarFilePath, err)
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
		return "", fmt.Errorf("failed to write content to %s: %v", rosaTarFilePath, err)
	}

	tarFileReader, err := os.Open(rosaTarFilePath)
	if err != nil {
		return "", fmt.Errorf("failed to open %s: %v", rosaTarFilePath, err)
	}
	defer tarFileReader.Close()

	gzipReader, err := gzip.NewReader(tarFileReader)
	if err != nil {
		return "", fmt.Errorf("failed to create gzip reader for %s: %v", rosaTarFilePath, err)
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

// getVersion gets the rosa cli version
func getVersion(ctx context.Context, rosaBinary string) (string, error) {
	stdout, _, err := cmd.Run(exec.CommandContext(ctx, rosaBinary, "version"))
	if err != nil {
		return "", err
	}

	versionSlice := strings.SplitAfter(fmt.Sprint(stdout), "\n")
	if len(versionSlice) == 0 {
		return "", errors.New("getVersion failed to get version from cli standard out")
	}

	return strings.ReplaceAll(versionSlice[0], "\n", ""), nil
}

// verifyLogin validates the authentication details provided are valid by logging in with rosa cli
func verifyLogin(ctx context.Context, rosaBinary string, token string, clientID string, clientSecret string, ocmEnvironment ocmclient.Environment, awsCredentials *awscloud.AWSCredentials) error {
	commandArgs := []string{"login"}

	command := exec.CommandContext(ctx, rosaBinary, commandArgs...)
	command.Env = append(command.Environ(), awsCredentials.CredentialsAsList()...)

	if clientID != "" && clientSecret != "" {
		command.Args = append(command.Args, "--client-id", clientID)
		command.Args = append(command.Args, "--client-secret", clientSecret)
		command.Args = append(command.Args, "--govcloud")
		// TODO: Work around. The rosa cli for govcloud does not support the --env passing the api endpoint.
		// The environment selection can be handled with a data structure that maps the environment to the api endpoint.
		if ocmEnvironment == "https://api.int.openshiftusgov.com" {
			ocmEnvironment = "integration"
		}
	} else {
		command.Args = append(command.Args, "--token", token)
		command.Env = append(command.Env, fmt.Sprintf("OCM_CONFIG=%s/ocm.json", os.TempDir()))
	}
	command.Args = append(command.Args, "--env", string(ocmEnvironment))
	command.Args = append(command.Args, "--region", string(awsCredentials.Region))

	_, stderr, err := cmd.Run(command)
	if err != nil {
		return fmt.Errorf("login failed with %q: %w", stderr, err)
	}

	return nil
}

// New handles constructing the rosa provider which creates a connection
// to openshift cluster manager "ocm". It is the callers responsibility
// to close the ocm connection when they are finished (defer provider.Connection.Close())
func New(ctx context.Context, token string, clientID string, clientSecret string, debug string, ocmEnvironment ocmclient.Environment, logger logr.Logger, args ...*awscloud.AWSCredentials) (*Provider, error) {
	if ocmEnvironment == "" || (token == "" && (clientID == "" || clientSecret == "")) {
		return nil, &providerError{err: errors.New("some parameters are undefined, unable to construct osd provider")}
	}

	rosaBinary, err := cliCheck()
	if err != nil {
		return nil, &providerError{err: err}
	}

	if debug == "true" {
		os.Setenv("ROSA_DEBUG", "true")
	}

	version, err := getVersion(ctx, rosaBinary)
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
	isFedRamp := strings.Contains(awsCredentials.Region, "gov")

	err = verifyLogin(ctx, rosaBinary, token, clientID, clientSecret, ocmEnvironment, awsCredentials)
	if err != nil {
		return nil, &providerError{err: err}
	}

	provider := &Provider{
		awsCredentials: awsCredentials,
		fedRamp:        isFedRamp,
		ocmEnvironment: ocmEnvironment,
		rosaBinary:     rosaBinary,
		debug:          debug,
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

	provider.Client, err = ocmclient.New(ctx, token, clientID, clientSecret, ocmEnvironment)
	if err != nil {
		return nil, &providerError{err: err}
	}

	return provider, nil
}
