package assertions

import (
	"context"
	"log"

	"github.com/onsi/gomega"
	operatorv1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// EventuallyCsv returns true if given namespace contains given CSV spec display name, and the CSV status is "Succeeded"
// It can be used with the standard or custom gomega matchers, timeout and polling interval
//
//	EventuallyCsv(ctx, dynamicClient, operatorName, namespaceName).Should(BeTrue())
func EventuallyCsv(ctx context.Context, dynamicClient dynamic.DynamicClient, specDisplayName, namespace string) gomega.AsyncAssertion {
	return gomega.Eventually(func() bool {
		csvList, err := dynamicClient.Resource(
			schema.GroupVersionResource{
				Group:    "operators.coreos.com",
				Version:  "v1alpha1",
				Resource: "clusterserviceversions",
			},
		).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("failed to get CSVs in namespace %s: %v", namespace, err)
			return false
		}
		for _, csv := range csvList.Items {
			specName, _, _ := unstructured.NestedFieldCopy(csv.Object, "spec", "displayName")
			statusPhase, _, _ := unstructured.NestedFieldCopy(csv.Object, "status", "phase")
			if statusPhase == string(operatorv1.CSVPhaseSucceeded) && specName == specDisplayName {
				return true
			}
		}
		return false
	})
}
