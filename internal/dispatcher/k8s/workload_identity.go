//go:build k8s

package k8s

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log"

	"k8s.io/client-go/kubernetes"

	"github.com/meshcloud/meshstack-cli/client"
)

const (
	azureAudience       = "api://AzureADTokenExchange"
	workloadIdentityDir = "/var/run/secrets/workload-identity/"
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
	if oidcIssuer == "" {
		return nil
	}
	tokens := newWorkloadIdentityTokens(namespace)
	return &client.MeshRunnerWorkloadIdentityFed{
		Issuer:          new(oidcIssuer),
		SubjectTemplate: new("system:serviceaccount:" + namespace + ":" + serviceAccountName("{{ workspaceIdentifier }}", "{{ buildingBlockDefinitionUuid }}")),
		Gcp:             tokens.gcp.registration(),
		Aws:             tokens.aws.registration(),
		Azure:           tokens.azure.registration(),
	}
}

func discoverOidcIssuer(ctx context.Context, clientset kubernetes.Interface, logger *log.Logger) string {
	issuer, err := fetchOidcIssuer(ctx, clientset)
	if err != nil {
		logger.Printf("OIDC issuer discovery failed, runs get no workload identity: %v", err)
		return ""
	}
	logger.Printf("Workload identity uses the OIDC issuer %s", issuer)
	return issuer
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
