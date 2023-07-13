package openshift

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/e2e-framework/klient/wait"
)

func (c *Client) UpgradeOperator(ctx context.Context, name, namespace string) error {
	dynamicClient, err := dynamic.NewForConfig(c.GetConfig())
	if err != nil {
		return err
	}

	var (
		csvs = dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "operators.coreos.com",
			Version:  "v1alpha1",
			Resource: "clusterserviceversions",
		}).Namespace(namespace)
		installplans = dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "operators.coreos.com",
			Version:  "v1alpha1",
			Resource: "installplans",
		}).Namespace(namespace)
		subscriptions = dynamicClient.Resource(schema.GroupVersionResource{
			Group:    "operators.coreos.com",
			Version:  "v1alpha1",
			Resource: "subscriptions",
		}).Namespace(namespace)
	)

	// get the current subscription
	subscription, err := subscriptions.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// find the csv name that matches the subscription name
	installedCSVs, err := csvs.List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	var latestCSV string
	for _, installedCSV := range installedCSVs.Items {
		displayName, _, err := unstructured.NestedString(installedCSV.Object, "spec", "displayName")
		if err != nil {
			return err
		}
		phase, _, err := unstructured.NestedString(installedCSV.Object, "status", "phase")
		if err != nil {
			return err
		}
		if displayName == name && phase == "Succeeded" {
			latestCSV = installedCSV.GetName()
		}
	}
	if len(latestCSV) == 0 {
		return fmt.Errorf("failed to find an installed CSV for %s", name)
	}

	// TODO: find n-1 CSV

	// uninstall operator
	if err = subscriptions.Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return err
	}

	if err = csvs.Delete(ctx, latestCSV, metav1.DeleteOptions{}); err != nil {
		return err
	}

	// wait until there is no install plan matching the name
	if err = wait.For(func() (bool, error) {
		ips, err := installplans.List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for _, installplan := range ips.Items {
			ipCSVs, _, err := unstructured.NestedStringSlice(installplan.Object, "spec", "clusterServiceVersionNames")
			if err != nil {
				return false, err
			}
			for _, ipCSV := range ipCSVs {
				if ipCSV == latestCSV {
					return false, nil
				}
			}
		}
		return true, nil
	}); err != nil {
		return err
	}

	pkg, _, err := unstructured.NestedString(subscription.Object, "spec", "package")
	if err != nil {
		return err
	}

	channel, _, err := unstructured.NestedString(subscription.Object, "spec", "channel")
	if err != nil {
		return err
	}

	catalogSource, _, err := unstructured.NestedString(subscription.Object, "spec", "catalogSource")
	if err != nil {
		return err
	}

	catalogSourceNamespace, _, err := unstructured.NestedString(subscription.Object, "spec", "catalogSourceNamespace")
	if err != nil {
		return err
	}

	// create a new subscription
	newSubscriptionObject := new(unstructured.Unstructured)
	newSubscriptionObject.SetUnstructuredContent(map[string]any{
		"apiVersion": "operators.coreos.com/v1alpha1",
		"kind":       "Subscription",
		"metadata": map[string]any{
			"name":      name,
			"namespace": namespace,
		},
		"spec": map[string]any{
			"package":                pkg,
			"channel":                channel,
			"catalogSource":          catalogSource,
			"catalogSourceNamespace": catalogSourceNamespace,
			"installPlanApproval":    "Automatic",
			"startingCSV":            "",
		},
	})
	if _, err = subscriptions.Create(ctx, newSubscriptionObject, metav1.CreateOptions{}); err != nil {
		return err
	}

	// wait for the install to succeed and that it is upgraded to the originally installed version
	if err = wait.For(func() (bool, error) {
		newSub, err := subscriptions.Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		currentCSV, _, err := unstructured.NestedString(newSub.Object, "status", "currentCSV")
		if err != nil {
			return false, err
		}
		phase, _, err := unstructured.NestedString(newSub.Object, "status", "phase")
		if err != nil {
			return false, err
		}
		return phase == "Succeeded" && currentCSV == latestCSV, nil
	}); err != nil {
		return err
	}

	return nil
}
