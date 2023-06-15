package assertions

import (
	"context"

	"github.com/go-logr/logr"
	. "github.com/onsi/gomega"
	"github.com/openshift/osde2e-common/pkg/clients/openshift"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// EventuallyCsv returns true if given namespace contains given CSV spec display name, and the CSV status is "Succeeded"
// It can be used with the standard or custom gomega matchers, timeout and polling interval
//
//	EventuallyCsv(ctx, dynamicClient, operatorName, namespaceName).Should(BeTrue())
func EventuallyCsv(ctx context.Context, specDisplayName, namespace string) AsyncAssertion {
	client, err := openshift.New(logr.Logger{})
	Expect(err).NotTo(HaveOccurred(), "Failed to create openshift client")
	dynamicClient, err := client.DynamicClient()
	Expect(err).NotTo(HaveOccurred(), "Failed to create dynamic client")

	return Eventually(func() bool {
		csvList, err := dynamicClient.Resource(
			schema.GroupVersionResource{
				Group:    "operators.coreos.com",
				Version:  "v1alpha1",
				Resource: "clusterserviceversions",
			},
		).Namespace(namespace).List(ctx, metav1.ListOptions{})
		Expect(err).NotTo(HaveOccurred(), "Failed to retrieve CSV from namespace %s", namespace)
		for _, csv := range csvList.Items {
			specName, _, _ := unstructured.NestedFieldCopy(csv.Object, "spec", "displayName")
			statusPhase, _, _ := unstructured.NestedFieldCopy(csv.Object, "status", "phase")
			if statusPhase == "Succeeded" && specName == specDisplayName {
				return true
			}
		}
		return false
	})
}
