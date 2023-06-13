package assertions

import (
	"context"
	"log"

	"github.com/onsi/gomega"
	operatorv1 "github.com/operator-framework/api/pkg/operators/v1alpha1"
	olm "github.com/operator-framework/operator-lifecycle-manager/pkg/api/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EventuallyCsv is a gomega async assertion that can be used with the
// standard or custom gomega matchers, timeout and polling interval
// It returns true if given operator olm clientset contains given operator csv
//
//	EventuallyCsv(ctx, clientset, operatorName, namespaceName).Should(BeTrue())
func EventuallyCsv(ctx context.Context, clientset *olm.Clientset, name, namespace string) gomega.AsyncAssertion {
	return gomega.Eventually(func() bool {
		csvList, err := clientset.OperatorsV1alpha1().ClusterServiceVersions(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			log.Printf("failed to get CSVs in namespace %s: %v", namespace, err)
			return false
		}
		for _, csv := range csvList.Items {
			if csv.Spec.DisplayName == name && csv.Status.Phase == operatorv1.CSVPhaseSucceeded {
				return true
			}
		}
		return false
	})
}
