package aws

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	secretsmanagertypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
	"github.com/go-logr/logr"
)

const (
	leftoverNameMarker = "osde2e"
	rosaOIDCSecretPref = "rosa-private-key-oidc-"
)

var secretClusterRE = regexp.MustCompile(`osde2e-[^-]+`)

// secretIdentity is the subset of a Secrets Manager secret used for cleanup decisions.
type secretIdentity struct {
	Name        string
	ARN         string
	Description string
	TagText     string
}

// blob concatenates name, ARN, description, and tags for leftover and skip matching.
func (s secretIdentity) blob() string {
	return strings.Join([]string{s.Name, s.ARN, s.Description, s.TagText}, " ")
}

// secretIdentityFromAWS maps an AWS Secrets Manager list entry into a secretIdentity.
func secretIdentityFromAWS(secret secretsmanagertypes.SecretListEntry) secretIdentity {
	tags := make([]string, 0, len(secret.Tags))
	for _, tag := range secret.Tags {
		tags = append(tags, aws.ToString(tag.Key)+"="+aws.ToString(tag.Value))
	}
	return secretIdentity{
		Name:        aws.ToString(secret.Name),
		ARN:         aws.ToString(secret.ARN),
		Description: aws.ToString(secret.Description),
		TagText:     strings.Join(tags, " "),
	}
}

// belongsToActiveCluster reports whether a secret belongs to a live osde2e cluster.
func belongsToActiveCluster(secret secretIdentity, activeClusters map[string]bool) (string, bool) {
	blob := secret.blob()
	for clusterName := range activeClusters {
		if clusterName != "" && strings.Contains(blob, clusterName) {
			return clusterName, true
		}
	}
	if match := secretClusterRE.FindString(blob); match != "" && activeClusters[match] {
		return match, true
	}
	return "", false
}

// isLeftoverSecret reports whether a secret looks like an osde2e leftover.
// Unmanaged ROSA OIDC configs store the RSA private key as rosa-private-key-oidc-*.
// That prefix is shared with any unmanaged ROSA cluster in the account, so it is
// not enough by itself: the secret must also carry an osde2e ownership marker
// (name, ARN, description, or tag such as owner=osde2e).
func isLeftoverSecret(secret secretIdentity) bool {
	return strings.Contains(strings.ToLower(secret.blob()), leftoverNameMarker)
}

// matchesOIDCSecretARN reports whether this secret is a live unmanaged OIDC private key.
// OCM SecretArn is the source of truth. AWS list ARNs often append a 6-character suffix
// to the name portion; match exact ARNs or the same secret name plus that suffix.
func matchesOIDCSecretARN(secret secretIdentity, activeOIDCSecretARNs map[string]bool) bool {
	if activeOIDCSecretARNs == nil || secret.ARN == "" {
		return false
	}
	if activeOIDCSecretARNs[secret.ARN] {
		return true
	}
	for arn := range activeOIDCSecretARNs {
		if oidcARNsMatch(secret.ARN, arn) {
			return true
		}
	}
	return false
}

// oidcARNsMatch reports whether a listed secret ARN is the same secret as an OCM skip ARN.
// AWS list ARNs often append a hyphen and 6 random characters to the name portion.
func oidcARNsMatch(secretARN, skipARN string) bool {
	if secretARN == "" || skipARN == "" {
		return false
	}
	if secretARN == skipARN {
		return true
	}
	a := secretNameFromARN(secretARN)
	b := secretNameFromARN(skipARN)
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	return awsSecretARNNameHasSuffix(a, b) || awsSecretARNNameHasSuffix(b, a)
}

// secretNameFromARN returns the secret name portion of a Secrets Manager ARN.
func secretNameFromARN(arn string) string {
	const marker = ":secret:"
	i := strings.LastIndex(arn, marker)
	if i < 0 {
		return ""
	}
	return arn[i+len(marker):]
}

// awsSecretARNNameHasSuffix reports whether full is base plus AWS's hyphen and 6-character suffix.
func awsSecretARNNameHasSuffix(full, base string) bool {
	return strings.HasPrefix(full, base+"-") && len(full) == len(base)+7
}

// isUnmanagedOIDCSecret reports whether the secret name is an unmanaged ROSA OIDC private key.
func isUnmanagedOIDCSecret(secret secretIdentity) bool {
	return strings.HasPrefix(secret.Name, rosaOIDCSecretPref)
}

// shouldSkipSecret reports whether a leftover must be kept. A nil OIDC skip set is
// fail-closed for unmanaged ROSA OIDC keys; an empty set means no live OIDC keys.
func shouldSkipSecret(secret secretIdentity, activeClusters map[string]bool, activeOIDCSecretARNs map[string]bool) (string, bool) {
	// Fail closed: never delete unmanaged OIDC keys unless the caller supplied an OCM ARN set.
	if isUnmanagedOIDCSecret(secret) && activeOIDCSecretARNs == nil {
		return "oidc skip set not provided", true
	}
	if matchesOIDCSecretARN(secret, activeOIDCSecretARNs) {
		return secret.ARN, true
	}
	return belongsToActiveCluster(secret, activeClusters)
}

// tooNewToDelete reports whether a leftover is within the age floor.
// Unmanaged OIDC keys are created before the cluster appears in OCM; without a
// cutoff those in-progress keys look like orphans. A missing CreatedDate is
// treated as too new. olderThan <= 0 disables the floor.
func tooNewToDelete(created *time.Time, olderThan time.Duration, now time.Time) (string, bool) {
	if olderThan <= 0 {
		return "", false
	}
	if created == nil {
		return "unknown created date", true
	}
	if now.Sub(*created) < olderThan {
		return "newer than cutoff", true
	}
	return "", false
}

// CleanupSecretsInput configures account-wide Secrets Manager leftover cleanup.
type CleanupSecretsInput struct {
	Config               aws.Config
	ActiveClusters       map[string]bool
	ActiveOIDCSecretARNs map[string]bool
	OlderThan            time.Duration
	DryRun               bool
	Log                  logr.Logger
}

// CleanupResult holds Secrets Manager leftover cleanup counters and per-secret errors.
type CleanupResult struct {
	Deleted int
	Failed  int
	Errors  []string
}

type secretsAPI interface {
	ListSecrets(ctx context.Context, params *secretsmanager.ListSecretsInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.ListSecretsOutput, error)
	DeleteSecret(ctx context.Context, params *secretsmanager.DeleteSecretInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.DeleteSecretOutput, error)
}

type regionsAPI interface {
	DescribeRegions(ctx context.Context, params *ec2.DescribeRegionsInput, optFns ...func(*ec2.Options)) (*ec2.DescribeRegionsOutput, error)
}

type cleanupSecretsDeps struct {
	regions   []string
	ec2       regionsAPI
	newClient func(region string) secretsAPI
}

// CleanupSecrets deletes leftover Secrets Manager secrets created by osde2e ROSA STS/HCP
// provision (unmanaged OIDC private keys and CAPA userdata) that are not tied to a live cluster.
// Unmanaged OIDC keys are only deleted when OlderThan > 0 (in-progress provision writes
// the secret before the cluster exists in OCM).
func CleanupSecrets(ctx context.Context, in CleanupSecretsInput) (CleanupResult, error) {
	return cleanupSecrets(ctx, in, cleanupSecretsDeps{})
}

// cleanupSecrets walks regions and deletes leftover secrets using the given dependencies.
func cleanupSecrets(ctx context.Context, in CleanupSecretsInput, deps cleanupSecretsDeps) (CleanupResult, error) {
	if in.Log.GetSink() == nil {
		in.Log = logr.Discard()
	}
	if in.ActiveClusters == nil {
		in.ActiveClusters = map[string]bool{}
	}

	regions, err := listSecretCleanupRegions(ctx, in, deps)
	if err != nil {
		return CleanupResult{}, err
	}

	newClient := deps.newClient
	if newClient == nil {
		newClient = func(region string) secretsAPI {
			cfg := in.Config.Copy()
			cfg.Region = region
			return secretsmanager.NewFromConfig(cfg)
		}
	}

	var result CleanupResult
	for _, region := range regions {
		regionResult, regionErr := cleanupSecretsInRegion(ctx, in, region, newClient(region))
		result.Deleted += regionResult.Deleted
		result.Failed += regionResult.Failed
		result.Errors = append(result.Errors, regionResult.Errors...)
		if regionErr != nil {
			return result, regionErr
		}
	}
	return result, nil
}

// listSecretCleanupRegions returns enabled AWS regions to scan, or the session region on DescribeRegions failure.
func listSecretCleanupRegions(ctx context.Context, in CleanupSecretsInput, deps cleanupSecretsDeps) ([]string, error) {
	if len(deps.regions) > 0 {
		return deps.regions, nil
	}

	client := deps.ec2
	if client == nil {
		client = ec2.NewFromConfig(in.Config)
	}

	out, err := client.DescribeRegions(ctx, &ec2.DescribeRegionsInput{
		AllRegions: aws.Bool(false),
	})
	if err != nil {
		region := in.Config.Region
		if region == "" {
			return nil, fmt.Errorf("list aws regions: %w", err)
		}
		in.Log.Info("DescribeRegions failed; falling back to session region", "error", err, "region", region)
		return []string{region}, nil
	}

	regions := make([]string, 0, len(out.Regions))
	for _, region := range out.Regions {
		if name := aws.ToString(region.RegionName); name != "" {
			regions = append(regions, name)
		}
	}
	if len(regions) == 0 {
		region := in.Config.Region
		if region == "" {
			return nil, fmt.Errorf("no aws regions available for secrets cleanup")
		}
		return []string{region}, nil
	}
	return regions, nil
}

// cleanupSecretsInRegion lists Secrets Manager secrets in one region and deletes leftovers that are safe to remove.
func cleanupSecretsInRegion(ctx context.Context, in CleanupSecretsInput, region string, client secretsAPI) (CleanupResult, error) {
	var result CleanupResult
	paginator := secretsmanager.NewListSecretsPaginator(client, &secretsmanager.ListSecretsInput{
		IncludePlannedDeletion: aws.Bool(true),
	})

	for paginator.HasMorePages() {
		page, pageErr := paginator.NextPage(ctx)
		if pageErr != nil {
			return result, fmt.Errorf("list secrets in %s: %w", region, pageErr)
		}
		for _, secret := range page.SecretList {
			ident := secretIdentityFromAWS(secret)
			if secret.DeletedDate != nil {
				continue
			}
			if !isLeftoverSecret(ident) {
				continue
			}
			if reason, ok := shouldSkipSecret(ident, in.ActiveClusters, in.ActiveOIDCSecretARNs); ok {
				in.Log.Info("Skipping secret for live cluster or OIDC config", "reason", reason, "secret", ident.Name, "region", region)
				continue
			}
			if isUnmanagedOIDCSecret(ident) && in.OlderThan <= 0 {
				in.Log.Info("Skipping unmanaged OIDC secret; age floor not provided", "secret", ident.Name, "region", region)
				continue
			}
			if reason, ok := tooNewToDelete(secret.CreatedDate, in.OlderThan, time.Now()); ok {
				in.Log.Info("Skipping secret newer than cutoff", "reason", reason, "secret", ident.Name, "region", region, "olderThan", in.OlderThan)
				continue
			}

			in.Log.Info("Secret will be deleted", "secret", ident.Name, "region", region)
			if in.DryRun {
				continue
			}

			_, delErr := client.DeleteSecret(ctx, &secretsmanager.DeleteSecretInput{
				SecretId:                   aws.String(ident.ARN),
				ForceDeleteWithoutRecovery: aws.Bool(true),
			})
			if delErr != nil {
				result.Failed++
				msg := fmt.Sprintf("secret %s (%s): not deleted: %v", ident.Name, region, delErr)
				in.Log.Error(delErr, "Failed to delete secret", "secret", ident.Name, "region", region)
				result.Errors = append(result.Errors, msg)
				continue
			}
			result.Deleted++
			in.Log.Info("Deleted secret", "secret", ident.Name, "region", region)
		}
	}

	return result, nil
}
