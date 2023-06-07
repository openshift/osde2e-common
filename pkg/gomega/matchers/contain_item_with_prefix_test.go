package matchers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ = Describe("list", func() {
	It("should contain the item", func() {
		list := &rbacv1.RoleList{
			Items: []rbacv1.Role{
				{ObjectMeta: metav1.ObjectMeta{Name: "test123"}},
			},
		}
		Expect(list).Should(ContainItemWithPrefix("test"))
	})

	It("should not contain the item", func() {
		list := &appsv1.DeploymentList{
			Items: []appsv1.Deployment{
				{ObjectMeta: metav1.ObjectMeta{Name: "brady"}},
			},
		}
		Expect(list).ShouldNot(ContainItemWithPrefix("test"))
	})
})
