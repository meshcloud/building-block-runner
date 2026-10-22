//go:build k8s

package k8s

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"

	"github.com/meshcloud/meshstack-cli/client"
	"k8s.io/client-go/kubernetes"
)

const (
	azureAudience       = "api://AzureADTokenExchange"
	workloadIdentityDir = "/var/run/secrets/workload-identity/"

	keycloakWifVolumeName = "meshstack-wif-token"
	keycloakWifTokenDir   = workloadIdentityDir + "meshstack"
	keycloakWifTokenPath  = keycloakWifTokenDir + "/token"
)

type workloadIdentityToken struct {
	provider string
	audience string
}

func (t workloadIdentityToken) volumeName() string {
	return "service-account-token-" + t.provider
}

func (t workloadIdentityToken) mountPath() string {
	return workloadIdentityDir + t.provider
}

func (t workloadIdentityToken) registration() *client.MeshRunnerWifProviderConfig {
	return &client.MeshRunnerWifProviderConfig{Audience: t.audience, TokenPath: t.mountPath() + "/token"}
}

type workloadIdentityTokens struct {
	gcp, aws, azure workloadIdentityToken
}

func newWorkloadIdentityTokens(namespace string) workloadIdentityTokens {
	return workloadIdentityTokens{
		gcp:   workloadIdentityToken{"gcp", "gcp-workload-identity-provider:" + namespace},
		aws:   workloadIdentityToken{"aws", "aws-workload-identity-provider:" + namespace},
		azure: workloadIdentityToken{"azure", azureAudience},
	}
}

func (ts workloadIdentityTokens) all() []workloadIdentityToken {
	return []workloadIdentityToken{ts.azure, ts.gcp, ts.aws}
}

// serviceAccountName separates its parts with periods, as a workspace identifier may contain dashes.
func serviceAccountName(workspace, definitionUuid string) string {
	return fmt.Sprintf("workspace.%s.buildingblockdefinition.%s", workspace, definitionUuid)
}

// newWorkloadIdentity renders a subject that matches the token's sub claim, which names the service account of the Job.
func newWorkloadIdentity(namespace, oidcIssuer string) *client.MeshRunnerWorkloadIdentityFed {
	tokens := newWorkloadIdentityTokens(namespace)
	return &client.MeshRunnerWorkloadIdentityFed{
		Issuer:          new(oidcIssuer),
		SubjectTemplate: new("system:serviceaccount:" + namespace + ":" + serviceAccountName("{{ workspaceIdentifier }}", "{{ buildingBlockDefinitionUuid }}")),
		Gcp:             tokens.gcp.registration(),
		Aws:             tokens.aws.registration(),
		Azure:           tokens.azure.registration(),
	}
}

func (d *Dispatcher) workloadIdentity(ctx context.Context) *client.MeshRunnerWorkloadIdentityFed {
	issuer, err := d.fetchOidcIssuer(ctx)
	if err != nil {
		slog.WarnContext(ctx, "OIDC issuer discovery failed, runs get no workload identity", "dispatcher", "k8s", "error", err)
		return nil
	}
	slog.InfoContext(ctx, "workload identity uses the OIDC issuer", "dispatcher", "k8s", "issuer", issuer)
	return newWorkloadIdentity(d.cfg.K8s.Namespace, issuer)
}

func fetchOidcIssuer(ctx context.Context, clientset kubernetes.Interface) (string, error) {
	body, err := clientset.Discovery().RESTClient().Get().AbsPath("/.well-known/openid-configuration").DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("fetch the OIDC configuration: %w", err)
	}
	var oidcConfig struct {
		Issuer string `json:"issuer"`
	}
	if err := json.Unmarshal(body, &oidcConfig); err != nil {
		return "", fmt.Errorf("parse the OIDC configuration: %w", err)
	}
	if oidcConfig.Issuer == "" {
		return "", errors.New("the OIDC configuration has no issuer")
	}
	return oidcConfig.Issuer, nil
}
