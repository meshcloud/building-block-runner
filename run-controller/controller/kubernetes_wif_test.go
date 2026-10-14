package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const testWifToken = "eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJiYmQifQ.c2lnbmF0dXJl"

type fakeWifTokenIssuer struct {
	err      error
	subjects []string
}

func (f *fakeWifTokenIssuer) Issue(_ context.Context, _ string, subject string) (string, error) {
	f.subjects = append(f.subjects, subject)
	if f.err != nil {
		return "", f.err
	}
	return testWifToken, nil
}

type fakeWifTokenWarner struct {
	runIds  []string
	reasons []string
}

func (f *fakeWifTokenWarner) ReportWifTokenMissing(_ context.Context, runId string, reason string) error {
	f.runIds = append(f.runIds, runId)
	f.reasons = append(f.reasons, reason)
	return nil
}

var testRunInfo = meshapi.RunInfo{
	Uuid:                             "8f0b6d6e-run",
	WorkspaceIdentifier:              "app-team",
	BuildingBlockDefinitionUuid:      "0d43a2b8-bbd",
	BuildingBlockDefinitionWorkspace: "platform-team",
}

func newTestKubernetesClient(t *testing.T, issuer *fakeWifTokenIssuer) (*KubernetesClient, *fakeWifTokenWarner, *bytes.Buffer) {
	prev := AppConfig
	AppConfig = &ControllerConfig{Namespace: "meshstack", Uuid: "controller-uuid"}
	t.Cleanup(func() { AppConfig = prev })

	logs := &bytes.Buffer{}
	warner := &fakeWifTokenWarner{}
	client := &KubernetesClient{
		clientset:   fake.NewClientset(),
		namespace:   "meshstack",
		logger:      log.New(logs, "", 0),
		wifWarnings: warner,
	}
	if issuer != nil {
		client.wifTokens = issuer
	}
	return client, warner, logs
}

func createTestJob(t *testing.T, client *KubernetesClient, implType string) *corev1.PodSpec {
	t.Helper()
	runJson := base64.StdEncoding.EncodeToString([]byte(`{}`))
	require.NoError(t, client.CreateRunnerJob(testRunInfo, runJson, implType, &JobSpecTemplate{Image: "runner"}, NewMetricsCollector()))
	job, err := client.clientset.BatchV1().Jobs("meshstack").Get(context.Background(), "runner-"+testRunInfo.Uuid, metav1.GetOptions{})
	require.NoError(t, err)
	return &job.Spec.Template.Spec
}

func wifTokenSecret(t *testing.T, client *KubernetesClient) (*corev1.Secret, error) {
	t.Helper()
	return client.clientset.CoreV1().Secrets("meshstack").Get(context.Background(), "wif-token-"+testRunInfo.Uuid, metav1.GetOptions{})
}

func assertWifTokenMountedOptionally(t *testing.T, pod *corev1.PodSpec) {
	t.Helper()
	var volume *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == wifTokenVolumeName {
			volume = &pod.Volumes[i]
		}
	}
	require.NotNil(t, volume)
	require.NotNil(t, volume.Secret)
	assert.Equal(t, "wif-token-"+testRunInfo.Uuid, volume.Secret.SecretName)
	assert.True(t, *volume.Secret.Optional)
	assert.Contains(t, pod.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      wifTokenVolumeName,
		MountPath: "/var/run/secrets/workload-identity/meshstack",
		ReadOnly:  true,
	})
}

func Test_TerraformRun_TokenOfBbdSubjectInJobSecret(t *testing.T) {
	issuer := &fakeWifTokenIssuer{}
	client, warner, _ := newTestKubernetesClient(t, issuer)

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Equal(t, []string{"bbd:platform-team:0d43a2b8-bbd"}, issuer.subjects)
	secret, err := wifTokenSecret(t, client)
	require.NoError(t, err)
	assert.Equal(t, testWifToken, string(secret.Data["token"]))
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, "runner-"+testRunInfo.Uuid, secret.OwnerReferences[0].Name)
	assertWifTokenMountedOptionally(t, pod)
	assert.Empty(t, warner.runIds)
}

func Test_TerraformRun_TokenNotLogged(t *testing.T) {
	client, _, logs := newTestKubernetesClient(t, &fakeWifTokenIssuer{})

	createTestJob(t, client, "TERRAFORM")

	assert.Contains(t, logs.String(), "Issued the meshStack WIF token")
	assert.NotContains(t, logs.String(), testWifToken)
}

func Test_IssuingFails_RunStartsWithoutTokenAndOwnerIsWarned(t *testing.T) {
	client, warner, _ := newTestKubernetesClient(t, &fakeWifTokenIssuer{err: errors.New("keycloak unreachable")})

	pod := createTestJob(t, client, "TERRAFORM")

	_, err := wifTokenSecret(t, client)
	assert.True(t, apierrors.IsNotFound(err))
	assertWifTokenMountedOptionally(t, pod)
	assert.Equal(t, []string{testRunInfo.Uuid}, warner.runIds)
	assert.Equal(t, []string{"keycloak unreachable"}, warner.reasons)
}

func Test_GitHubRun_NoTokenIssued(t *testing.T) {
	issuer := &fakeWifTokenIssuer{}
	client, warner, _ := newTestKubernetesClient(t, issuer)

	createTestJob(t, client, "GITHUB_WORKFLOW")

	assert.Empty(t, issuer.subjects)
	assert.Empty(t, warner.runIds)
}

func Test_WifTokenOff_RunStartsWithoutTokenOrWarning(t *testing.T) {
	client, warner, _ := newTestKubernetesClient(t, nil)

	pod := createTestJob(t, client, "TERRAFORM")

	_, err := wifTokenSecret(t, client)
	assert.True(t, apierrors.IsNotFound(err))
	assertWifTokenMountedOptionally(t, pod)
	assert.Empty(t, warner.runIds)
}

func Test_TokenSecretFromEarlierAttempt_Replaced(t *testing.T) {
	client, _, _ := newTestKubernetesClient(t, &fakeWifTokenIssuer{})
	secrets := client.clientset.CoreV1().Secrets("meshstack")
	_, err := secrets.Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "wif-token-" + testRunInfo.Uuid, Namespace: "meshstack"},
		Data:       map[string][]byte{"token": []byte("expired-token")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	createTestJob(t, client, "TERRAFORM")

	secret, err := wifTokenSecret(t, client)
	require.NoError(t, err)
	assert.Equal(t, testWifToken, string(secret.Data["token"]))
}
