package rosa

import (
	"testing"

	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

func TestOidcSecretARNHasPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		arn    string
		prefix string
		want   bool
	}{
		{
			name:   "exact secret name",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abc",
			prefix: "abc",
			want:   true,
		},
		{
			name:   "aws six char suffix",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abc-AbCdEf",
			prefix: "abc",
			want:   true,
		},
		{
			name:   "prefix does not match longer sibling",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abcd",
			prefix: "abc",
			want:   false,
		},
		{
			name:   "hyphenated cluster name does not match sibling",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-osde2e-abcd",
			prefix: "osde2e-abc",
			want:   false,
		},
		{
			name:   "hyphenated cluster name exact",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-osde2e-abc",
			prefix: "osde2e-abc",
			want:   true,
		},
		{
			name:   "empty prefix",
			arn:    "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abc",
			prefix: "",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := oidcSecretARNHasPrefix(tt.arn, tt.prefix); got != tt.want {
				t.Fatalf("oidcSecretARNHasPrefix(%q, %q) = %v, want %v", tt.arn, tt.prefix, got, tt.want)
			}
		})
	}
}

func TestSelectOIDCConfigByPrefix(t *testing.T) {
	t.Parallel()

	abc := mustRosaOidc(t, "oidc-z", "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abc")
	abcd := mustRosaOidc(t, "oidc-a", "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abcd")
	abcOther := mustRosaOidc(t, "oidc-m", "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-abc-AbCdEf")

	got := selectOIDCConfigByPrefix(map[string]*cmv1.OidcConfig{
		abcd.ID():     abcd,
		abcOther.ID(): abcOther,
		abc.ID():      abc,
	}, "abc")
	if got == nil {
		t.Fatal("selectOIDCConfigByPrefix() = nil, want a match")
	}
	if got.ID() != "oidc-m" {
		t.Fatalf("selectOIDCConfigByPrefix() id = %q, want oidc-m (lowest matching id)", got.ID())
	}
}

func mustRosaOidc(t *testing.T, id, secretARN string) *cmv1.OidcConfig {
	t.Helper()
	cfg, err := cmv1.NewOidcConfig().ID(id).SecretArn(secretARN).Managed(false).Build()
	if err != nil {
		t.Fatalf("build oidc config: %v", err)
	}
	return cfg
}
