package osd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Masterminds/semver/v3"
	clustersmgmtv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
)

const (
	managedUpgradeOperatorDeploymentName = "managed-upgrade-operator"
	managedUpgradeOperatorNamespace      = "openshift-managed-upgrade-operator"
	versionGateLabel                     = "api.openshift.com/gate-ocp"
	upgradeMaxAttempts                   = 1080
	upgradeDelay                         = 10
)

// upgradeError represents the cluster upgrade custom error
type upgradeError struct {
	err error
}

// Error returns the formatted error message when upgradeError is invoked
func (e *upgradeError) Error() string {
	return fmt.Sprintf("osd upgrade failed: %v", e.err)
}

// versionGates returns a list of available version gates from ocm
func (o *Provider) versionGates(ctx context.Context) (*clustersmgmtv1.VersionGateList, error) {
	response, err := o.ClustersMgmt().V1().VersionGates().List().SendContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get version gates: %v", err)
	}

	return response.Items(), nil
}

// getVersionGateID returns the version gate agreement id
func (o *Provider) getVersionGateID(ctx context.Context, version string) (string, error) {
	versionGates, err := o.versionGates(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get version gate id for version %q, %v", version, err)
	}

	for _, versionGate := range versionGates.Slice() {
		if versionGate.VersionRawIDPrefix() == version && versionGate.Label() == versionGateLabel {
			return versionGate.ID(), nil
		}
	}

	return "", fmt.Errorf("no version gate exists for %q", version)
}

// getVersionGateAgreement returns the gate agreement ocm resource
func (o *Provider) getVersionGateAgreement(ctx context.Context, versionGateID string) (*clustersmgmtv1.VersionGate, error) {
	response, err := o.ClustersMgmt().V1().VersionGates().VersionGate(versionGateID).Get().SendContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed  to get version gate agreement %q, %v", versionGateID, err)
	}

	return response.Body(), nil
}

// gateAgreementExistForCluster checks to see if the version gate agreement id provided for the cluster already exists
func (o *Provider) gateAgreementExistForCluster(ctx context.Context, clusterID, gateAgreementID string) (bool, error) {
	response, err := o.ClustersMgmt().V1().Clusters().Cluster(clusterID).GateAgreements().List().SendContext(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get cluster %q version gate agreement: %v", clusterID, err)
	}

	for _, gateAgreement := range response.Items().Slice() {
		if gateAgreement.VersionGate().ID() == gateAgreementID {
			o.log.Info("Gate agreement exists", clusterIDLoggerKey, clusterID, "gate_agreement_id", gateAgreementID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			return true, nil
		}
	}

	return false, nil
}

// addGateAgreement adds a version gate agreement to the cluster ocm resource.
// Version gate agreement are used to acknowledge the cluster can be upgraded between versions
func (o *Provider) addGateAgreement(ctx context.Context, clusterID string, currentVersion, upgradeVersion semver.Version) error {
	if currentVersion.Minor() > upgradeVersion.Minor() {
		o.log.Info("Gate agreement not required for z stream upgrades", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
		return nil
	}

	majorMinor := fmt.Sprintf("%d.%d", upgradeVersion.Major(), upgradeVersion.Minor())

	versionGateID, err := o.getVersionGateID(ctx, majorMinor)
	if err != nil {
		return err
	}

	exist, err := o.gateAgreementExistForCluster(ctx, clusterID, versionGateID)
	if err != nil {
		return err
	}
	if exist {
		return nil
	}

	gateAgreement, err := o.getVersionGateAgreement(ctx, versionGateID)
	if err != nil {
		return err
	}

	versionGateAgreement, err := clustersmgmtv1.NewVersionGateAgreement().
		VersionGate(clustersmgmtv1.NewVersionGate().Copy(gateAgreement)).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build version gate agreement for cluster %q, %v", clusterID, err)
	}

	_, err = o.ClustersMgmt().V1().Clusters().Cluster(clusterID).GateAgreements().Add().Body(versionGateAgreement).SendContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to apply version gate agreement to cluster %q, %v", clusterID, err)
	}

	return nil
}

// initiateUpgrade initiates the upgrade for the cluster with ocm by applying a upgrade policy to the cluster
func (o *Provider) initiateUpgrade(ctx context.Context, clusterID, version string) error {
	upgradePolicy, err := clustersmgmtv1.NewUpgradePolicy().Version(version).
		NextRun(time.Now().UTC().Add(7 * time.Minute)).
		ScheduleType("manual").Build()
	if err != nil {
		return fmt.Errorf("failed to build upgrade policy for cluster %q, %v", clusterID, err)
	}

	response, err := o.ClustersMgmt().V1().Clusters().Cluster(clusterID).UpgradePolicies().Add().Body(upgradePolicy).SendContext(ctx)
	if err != nil || response.Status() != http.StatusCreated {
		return fmt.Errorf("failed to apply upgrade policy to cluster %q, %v", clusterID, err)
	}

	o.log.Info("Cluster upgrade scheduled!", clusterIDLoggerKey, clusterID, "upgrade_version", response.Body().Version(),
		"upgradeTime", response.Body().NextRun().Format(time.RFC3339), ocmEnvironmentLoggerKey, o.ocmEnvironment)

	return nil
}

// restartManagedUpgradeOperator scales down/up the muo operator to speed up the cluster upgrade start time
func (o *Provider) restartManagedUpgradeOperator(ctx context.Context, client *openshift.Client) error {
	patchReplicas := func(replicasCount int) (*k8s.Patch, error) {
		patchData, err := json.Marshal(map[string]interface{}{
			"spec": map[string]interface{}{
				"replicas": replicasCount,
			},
		})
		if err != nil {
			return nil, fmt.Errorf("failed to build patch data to modify deployment %s replicas count: %v", managedUpgradeOperatorDeploymentName, err)
		}

		return &k8s.Patch{
			PatchType: types.StrategicMergePatchType,
			Data:      patchData,
		}, nil
	}

	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: managedUpgradeOperatorDeploymentName, Namespace: managedUpgradeOperatorNamespace}}
	err := wait.For(conditions.New(client.Resources).DeploymentConditionMatch(deployment, appsv1.DeploymentAvailable, corev1.ConditionTrue))
	if err != nil {
		return fmt.Errorf("failed to get managed upgrade operator deployment: %v", err)
	}

	patchData, err := patchReplicas(0)
	if err != nil {
		return err
	}

	err = client.Patch(ctx, deployment, *patchData)
	if err != nil {
		return fmt.Errorf("failed to scale down %s deployment: %v", managedUpgradeOperatorDeploymentName, err)
	}

	patchData, err = patchReplicas(1)
	if err != nil {
		return err
	}

	err = client.Patch(ctx, deployment, *patchData)
	if err != nil {
		return fmt.Errorf("failed to scale up %s deployment: %v", managedUpgradeOperatorDeploymentName, err)
	}

	o.log.Info("Managed upgrade operator restarted!")

	return nil
}

// managedUpgradeConfigExist waits/checks for the muo upgrade config to exist on the cluster
func (o *Provider) managedUpgradeConfigExist(ctx context.Context, dynamicClient *dynamic.DynamicClient) error {
	for i := 1; i <= 6; i++ {
		upgradeConfig, err := getManagedUpgradeOperatorConfig(ctx, dynamicClient)
		if err != nil || upgradeConfig == nil {
			time.Sleep(30 * time.Second)
			continue
		}
		return nil
	}

	return fmt.Errorf("managed upgrade config does not exist the cluster")
}

// OCMUpgrade handles the end to end process to upgrade an openshift dedicated cluster
func (o *Provider) OCMUpgrade(ctx context.Context, client *openshift.Client, clusterID string, currentVersion, upgradeVersion semver.Version) error {
	var (
		conditionMessage string
		dynamicClient    *dynamic.DynamicClient
		err              error
		upgradeStatus    string
	)

	if dynamicClient, err = getKubernetesDynamicClient(client); err != nil {
		return &upgradeError{err: err}
	}

	if err = o.addGateAgreement(ctx, clusterID, currentVersion, upgradeVersion); err != nil {
		return &upgradeError{err: err}
	}

	if err = o.initiateUpgrade(ctx, clusterID, upgradeVersion.String()); err != nil {
		return &upgradeError{err: err}
	}

	if err = o.restartManagedUpgradeOperator(ctx, client); err != nil {
		return &upgradeError{err: err}
	}

	if err = o.managedUpgradeConfigExist(ctx, dynamicClient); err != nil {
		return &upgradeError{err: err}
	}

	errorHandler := func(key string, found bool, err error) error {
		if !found || err != nil {
			o.log.Error(err, "Managed upgrade operator config key is missing", "key", key)
			time.Sleep(10 * time.Second)
			return err
		}
		return nil
	}

	for i := 1; i <= upgradeMaxAttempts; i++ {
		upgradeConfig, err := getManagedUpgradeOperatorConfig(ctx, dynamicClient)
		if err != nil {
			o.log.Error(err, "Failed to get managed upgrade operator config")
			time.Sleep(upgradeDelay * time.Second)
			continue
		}

		status, found, err := unstructured.NestedMap(upgradeConfig.Object, "status")
		if errorHandler("status", found, err) != nil {
			continue
		}

		histories, found, err := unstructured.NestedSlice(status, "history")
		if errorHandler("status.history", found, err) != nil {
			continue
		}

		for _, h := range histories {
			version, found, err := unstructured.NestedString(h.(map[string]interface{}), "version")
			if errorHandler("status.history.[].version", found, err) != nil {
				continue
			}

			if version == upgradeVersion.String() {
				upgradeStatus, found, err = unstructured.NestedString(h.(map[string]interface{}), "phase")
				if errorHandler("status.history.[].version.phase", found, err) != nil {
					continue
				}

				conditions, found, err := unstructured.NestedSlice(h.(map[string]interface{}), "conditions")
				if (upgradeStatus == "Pending" && len(conditions) < 1) || len(conditions) < 1 {
					break
				}
				if errorHandler("status.history.[].version.conditions", found, err) != nil {
					continue
				}

				conditionMessage, found, err = unstructured.NestedString(conditions[0].(map[string]interface{}), "message")
				if errorHandler("status.history.[].version.message", found, err) != nil {
					continue
				}

				break
			}
		}

		switch upgradeStatus {
		case "":
			o.log.Info("Upgrade has not started yet...", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			time.Sleep(upgradeDelay * time.Second)
		case "Failed", clusterIDLoggerKey, clusterID:
			o.log.Info("Upgrade failed!", "condition_message", conditionMessage, clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			return &upgradeError{err: fmt.Errorf("upgrade failed")}
		case "Upgraded":
			o.log.Info("Upgrade complete!", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			return nil
		case "Pending":
			o.log.Info("Upgrade is pending...", clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			time.Sleep(upgradeDelay * time.Second)
		case "Upgrading":
			o.log.Info("Upgrade is in progress", "condition_message", conditionMessage, clusterIDLoggerKey, clusterID, ocmEnvironmentLoggerKey, o.ocmEnvironment)
			time.Sleep(upgradeDelay * time.Second)
		}
	}

	return fmt.Errorf("upgrade is still in progress, failed to finish within max wait attempts")
}

// getKubernetesDynamicClient returns the kubernetes dynamic client
func getKubernetesDynamicClient(client *openshift.Client) (*dynamic.DynamicClient, error) {
	dynamicClient, err := dynamic.NewForConfig(client.GetConfig())
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes dynamic client: %w", err)
	}
	return dynamicClient, nil
}

// getManagedUpgradeOperatorConfig returns the upgrade config object
func getManagedUpgradeOperatorConfig(ctx context.Context, dynamicClient *dynamic.DynamicClient) (*unstructured.Unstructured, error) {
	upgradeConfigs, err := dynamicClient.Resource(
		schema.GroupVersionResource{
			Group:    "upgrade.managed.openshift.io",
			Version:  "v1alpha1",
			Resource: "upgradeconfigs",
		},
	).Namespace(managedUpgradeOperatorNamespace).List(ctx, metav1.ListOptions{})
	if err != nil || len(upgradeConfigs.Items) < 1 {
		return nil, err
	}

	return &upgradeConfigs.Items[0], nil
}
