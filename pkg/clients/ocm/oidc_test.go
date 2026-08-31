package ocm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	ocmsdk "github.com/openshift-online/ocm-sdk-go"
	cmv1 "github.com/openshift-online/ocm-sdk-go/clustersmgmt/v1"
)

// mustCluster builds a clusters_mgmt Cluster for OIDC SecretArn tests.
func mustCluster(t *testing.T, id string, oidc *cmv1.OidcConfigBuilder) *cmv1.Cluster {
	t.Helper()
	builder := cmv1.NewCluster().ID(id)
	if oidc != nil {
		builder = builder.AWS(cmv1.NewAWS().STS(cmv1.NewSTS().OidcConfig(oidc)))
	}
	cluster, err := builder.Build()
	if err != nil {
		t.Fatalf("build cluster: %v", err)
	}
	return cluster
}

// mustOidc builds an OidcConfig for SecretArn lookup tests.
func mustOidc(t *testing.T, id, secretARN string, managed bool) *cmv1.OidcConfig {
	t.Helper()
	builder := cmv1.NewOidcConfig().ID(id).Managed(managed)
	if secretARN != "" {
		builder = builder.SecretArn(secretARN)
	}
	cfg, err := builder.Build()
	if err != nil {
		t.Fatalf("build oidc config: %v", err)
	}
	return cfg
}

func TestClusterOIDCSecretARN(t *testing.T) {
	t.Parallel()

	const (
		inlineARN = "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-aaa"
		lookupARN = "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-bbb"
	)

	tests := []struct {
		name     string
		cluster  *cmv1.Cluster
		oidcByID map[string]*cmv1.OidcConfig
		wantARN  string
		wantErr  string
	}{
		{
			name:    "nil cluster",
			wantARN: "",
		},
		{
			name:    "no aws sts",
			cluster: mustCluster(t, "c1", nil),
			wantARN: "",
		},
		{
			name:    "inline secret arn",
			cluster: mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-aaa").SecretArn(inlineARN).Managed(false)),
			wantARN: inlineARN,
		},
		{
			name:    "lookup by oidc config id",
			cluster: mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-bbb")),
			oidcByID: map[string]*cmv1.OidcConfig{
				"oidc-bbb": mustOidc(t, "oidc-bbb", lookupARN, false),
			},
			wantARN: lookupARN,
		},
		{
			name:    "managed oidc has no ccs secret",
			cluster: mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-managed")),
			oidcByID: map[string]*cmv1.OidcConfig{
				"oidc-managed": mustOidc(t, "oidc-managed", "", true),
			},
			wantARN: "",
		},
		{
			name:     "nil lookup set fails closed",
			cluster:  mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-aaa")),
			oidcByID: nil,
			wantErr:  "oidc config list not provided",
		},
		{
			name:     "missing oidc config",
			cluster:  mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-missing")),
			oidcByID: map[string]*cmv1.OidcConfig{},
			wantErr:  "oidc-missing",
		},
		{
			name:    "unmanaged oidc with no secret arn",
			cluster: mustCluster(t, "c1", cmv1.NewOidcConfig().ID("oidc-empty")),
			oidcByID: map[string]*cmv1.OidcConfig{
				"oidc-empty": mustOidc(t, "oidc-empty", "", false),
			},
			wantErr: "has no secret_arn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := ClusterOIDCSecretARN(tt.cluster, tt.oidcByID)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ClusterOIDCSecretARN() error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ClusterOIDCSecretARN() error = %v", err)
			}
			if got != tt.wantARN {
				t.Fatalf("ClusterOIDCSecretARN() = %q, want %q", got, tt.wantARN)
			}
		})
	}
}

func TestClusterOIDCSecretARNs(t *testing.T) {
	t.Parallel()

	const (
		inlineARN = "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-aaa"
		lookupARN = "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-bbb"
	)

	clusters := []*cmv1.Cluster{
		mustCluster(t, "c-inline", cmv1.NewOidcConfig().ID("oidc-aaa").SecretArn(inlineARN)),
		mustCluster(t, "c-lookup", cmv1.NewOidcConfig().ID("oidc-bbb")),
		mustCluster(t, "c-none", nil),
	}
	oidcByID := map[string]*cmv1.OidcConfig{
		"oidc-bbb": mustOidc(t, "oidc-bbb", lookupARN, false),
		"oidc-ccc": mustOidc(t, "oidc-ccc", "arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-other", false),
	}

	arns, err := ClusterOIDCSecretARNs(clusters, oidcByID)
	if err != nil {
		t.Fatalf("ClusterOIDCSecretARNs() error = %v", err)
	}
	if !arns[inlineARN] || !arns[lookupARN] {
		t.Fatalf("arns = %v, want inline and lookup", arns)
	}
	if arns["arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-other"] {
		t.Fatalf("unrelated oidc config should not be included: %v", arns)
	}
	if len(arns) != 2 {
		t.Fatalf("arns = %v, want 2 entries", arns)
	}
}

func TestOIDCSecretARNsEmptyClusters(t *testing.T) {
	t.Parallel()

	arns, err := OIDCSecretARNs(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("OIDCSecretARNs() error = %v", err)
	}
	if arns == nil || len(arns) != 0 {
		t.Fatalf("arns = %v, want empty map", arns)
	}
}

var testJWTKey *rsa.PrivateKey

// init generates the RSA key used to sign mocked OCM access tokens.
func init() {
	var err error
	testJWTKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
}

// testOCMConnection returns an OCM SDK connection pointed at an httptest TLS server.
func testOCMConnection(t *testing.T, handler http.Handler) *ocmsdk.Connection {
	t.Helper()
	server := httptest.NewTLSServer(handler)
	t.Cleanup(server.Close)

	conn, err := ocmsdk.NewConnectionBuilder().
		URL(server.URL).
		TokenURL(server.URL+"/token").
		Client("test", "test").
		TransportWrapper(func(_ http.RoundTripper) http.RoundTripper {
			return server.Client().Transport
		}).
		Insecure(true).
		Build()
	if err != nil {
		t.Fatalf("failed to build OCM connection: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// testAccessToken returns a signed JWT the mocked token endpoint can serve.
func testAccessToken(t *testing.T) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"typ": "Bearer",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(15 * time.Minute).Unix(),
		"iss": "https://sso.redhat.com/auth/realms/redhat-external",
	})
	token.Header["kid"] = "test-key"
	signed, err := token.SignedString(testJWTKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

// tokenHandler serves a token response with the given access token.
func tokenHandler(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"%s","token_type":"Bearer","expires_in":900}`, token)
	}
}

func TestListOIDCConfigsPaginated(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(testAccessToken(t)))
	mux.HandleFunc("/api/clusters_mgmt/v1/oidc_configs", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		page := r.URL.Query().Get("page")
		switch page {
		case "", "1":
			_, _ = fmt.Fprint(w, `{"kind":"OidcConfigList","page":1,"size":1,"total":2,"items":[{"kind":"OidcConfig","id":"oidc-1","secret_arn":"arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-1","managed":false}]}`)
		case "2":
			_, _ = fmt.Fprint(w, `{"kind":"OidcConfigList","page":2,"size":1,"total":2,"items":[{"kind":"OidcConfig","id":"oidc-2","secret_arn":"arn:aws:secretsmanager:us-east-1:1:secret:rosa-private-key-oidc-2","managed":false}]}`)
		default:
			_, _ = fmt.Fprintf(w, `{"kind":"OidcConfigList","page":%s,"size":0,"total":2,"items":[]}`, page)
		}
	})

	conn := testOCMConnection(t, mux)
	got, err := ListOIDCConfigs(context.Background(), conn)
	if err != nil {
		t.Fatalf("ListOIDCConfigs() error = %v", err)
	}
	if len(got) != 2 || got["oidc-1"] == nil || got["oidc-2"] == nil {
		t.Fatalf("ListOIDCConfigs() = %v, want oidc-1 and oidc-2", got)
	}
	if got["oidc-1"].SecretArn() == "" || got["oidc-2"].SecretArn() == "" {
		t.Fatalf("ListOIDCConfigs() missing secret_arn: %#v", got)
	}
}

func TestListOIDCConfigsError(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/token", tokenHandler(testAccessToken(t)))
	mux.HandleFunc("/api/clusters_mgmt/v1/oidc_configs", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprint(w, `{"kind":"Error","reason":"oidc configs down"}`)
	})

	conn := testOCMConnection(t, mux)
	_, err := ListOIDCConfigs(context.Background(), conn)
	if err == nil || !strings.Contains(err.Error(), "list oidc configs") {
		t.Fatalf("ListOIDCConfigs() error = %v, want list oidc configs", err)
	}
}
