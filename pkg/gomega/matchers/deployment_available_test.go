package matchers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

var _ = Describe("deployment", func() {
	It("should be available", func(ctx context.Context) {
		deployment := &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentAvailable,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}
		Expect(deployment).Should(BeAvailable())
	})

	It("should not be available", func(ctx context.Context) {
		deployment := &appsv1.Deployment{
			Status: appsv1.DeploymentStatus{
				Conditions: []appsv1.DeploymentCondition{
					{
						Type:   appsv1.DeploymentProgressing,
						Status: corev1.ConditionTrue,
					},
				},
			},
		}
		Expect(deployment).ShouldNot(BeAvailable())
	})
})
