package ocm

import (
	"github.com/onsi/gomega"
	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"
	quotav1 "github.com/openshift/api/quota/v1"
	route "github.com/openshift/api/route/v1"
	securityv1 "github.com/openshift/api/security/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
)

type User struct {
	ServiceAccount string
	Username       string
	Groups         []string
	RestConfig     *rest.Config
}

func (u *User) New(sa string, rc *rest.Config, un string, group []string) *User {
	return &User{ServiceAccount: sa, RestConfig: rc, Username: un, Groups: group}
}

// Returns a resource client using impersonated rest config. Either SA or a username and group name should be provided in User struct.
// To impersonate as SA
//
//	u := User{ServiceAccount: "sa", RestConfig: restconfig}
//	imperosnatedClient := u.NewImpersonatedClient()
//
// To impersonate as a group, e.g. dedicated-admins
//
//	u := User{Username: "test-user@redhat.com", Groups: []string{"dedicated-admins"} ,RestConfig: restconfig}
//	imperosnatedClient := u.NewImpersonatedClient()
func (u *User) NewImpersonatedClient() *resources.Resources {

	if u.Username != "" {
		// these groups are required for impersonating a user
		u.Groups = append(u.Groups, "system:authenticated", "system:authenticated:oauth")
	}

	u.Impersonate(rest.ImpersonationConfig{
		UserName: u.Username,
		Groups:   u.Groups,
	})

	client, err := resources.New(u.RestConfig)
	gomega.ExpectWithOffset(1, err).ShouldNot(gomega.HaveOccurred(), "failed to create openshift resources client")

	// register core openshift schemas
	configv1.AddToScheme(client.GetScheme())
	quotav1.AddToScheme(client.GetScheme())
	securityv1.AddToScheme(client.GetScheme())
	route.AddToScheme(client.GetScheme())
	imagev1.AddToScheme(client.GetScheme())

	return client
}

// Impersonate sets impersonate user headers
func (u *User) Impersonate(restImpersonConfig rest.ImpersonationConfig) *User {
	u.RestConfig.Impersonate = restImpersonConfig
	return u
}
