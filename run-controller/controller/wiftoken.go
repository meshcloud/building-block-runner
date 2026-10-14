package controller

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/meshcloud/building-block-runner/run-controller/keycloakwif"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type wifTokenIssuer interface {
	Issue(ctx context.Context, runUuid, subject string) (string, error)
}

type wifTokenWarner interface {
	ReportWifTokenMissing(ctx context.Context, runId string) error
}

const (
	wifTokenMountPath    = "/var/run/secrets/workload-identity/meshstack"
	wifTokenSecretKey    = "token"
	wifTokenPath         = wifTokenMountPath + "/" + wifTokenSecretKey
	wifTokenVolumeName   = "meshstack-wif-token"
	wifTokenIssueTimeout = 15 * time.Second
)

func newWifTokenIssuer(config *KeycloakWifConfig, logger *log.Logger) *keycloakwif.Issuer {
	return keycloakwif.NewIssuer(keycloakwif.Config{
		Url:           config.Url,
		Realm:         config.Realm,
		Issuer:        config.issuer(),
		ClientId:      config.ClientId,
		ClientSecret:  config.ClientSecret,
		TokenLifetime: time.Duration(config.TokenLifetimeMinutes) * time.Minute,
	}, &http.Client{Timeout: wifTokenIssueTimeout}, logger)
}

// createWifTokenSecret mints the run's token and stores it in a new Secret. It returns the Secret's name, or "" when
// the run gets no token.
func (k *KubernetesClient) createWifTokenSecret(namespace string, runInfo meshapi.RunInfo, implType string, metrics *MetricsCollector) string {
	if k.wifTokens == nil || implType != string(meshapi.RunnerTypeTerraform) {
		return ""
	}

	ctx, cancel := context.WithTimeout(context.Background(), wifTokenIssueTimeout)
	defer cancel()
	subject := renderSubjectTemplate(keycloakSubjectTemplate, runInfo)
	token, err := k.wifTokens.Issue(ctx, runInfo.Uuid, subject)
	var secretName string
	if err == nil {
		secretName, err = k.createTokenSecret(namespace, runInfo.Uuid, token)
	}
	if err != nil {
		metrics.wifTokenErrors.WithLabelValues(AppConfig.Uuid).Inc()
		k.logger.Printf("Run %s starts without the meshStack WIF token: %v", runInfo.Uuid, err)
		warnCtx, cancelWarn := context.WithTimeout(context.Background(), wifTokenIssueTimeout)
		defer cancelWarn()
		if warnErr := k.wifWarnings.ReportWifTokenMissing(warnCtx, runInfo.Uuid); warnErr != nil {
			k.logger.Printf("Failed to tell run %s that it has no meshStack WIF token: %v", runInfo.Uuid, warnErr)
		}
		return ""
	}

	metrics.wifTokensIssuedTotal.WithLabelValues(AppConfig.Uuid).Inc()
	k.logger.Printf("Issued the meshStack WIF token for run %s with subject %s in secret %s", runInfo.Uuid, subject, secretName)
	return secretName
}

func (k *KubernetesClient) createTokenSecret(namespace, runID, token string) (string, error) {
	secret := &corev1.Secret{
		// Kubernetes appends a random suffix, so a dispatch never mounts the token of an earlier dispatch of the same run.
		GenerateName: fmt.Sprintf("wif-token-%s-", runID),
		Namespace:    namespace,
		Labels: map[string]string{
			"app.kubernetes.io/name": "runner",
			"meshcloud.io/run-id":    runID,
			"meshcloud.io/runner-id": AppConfig.Uuid,
		},
		Data: map[string][]byte{wifTokenSecretKey: []byte(token)},
	}
	created, err := k.clientset.CoreV1().Secrets(namespace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to create secret: %w", err)
	}
	return created.Name, nil
}
