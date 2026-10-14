package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log"
	"strings"
	"testing"

	meshapi "github.com/meshcloud/building-block-runner/go-meshapi-client/meshapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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
	runIds []string
}

func (f *fakeWifTokenWarner) ReportWifTokenMissing(_ context.Context, runId string) error {
	f.runIds = append(f.runIds, runId)
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
	clientset := fake.NewClientset()
	// The fake clientset ignores GenerateName, so this reactor appends the random suffix like the API server does.
	clientset.PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret); secret.Name == "" {
			secret.Name = secret.GenerateName + utilrand.String(5)
		}
		return false, nil, nil
	})
	client := &KubernetesClient{
		clientset:   clientset,
		namespace:   "meshstack",
		logger:      log.New(logs, "", 0),
		wifWarnings: warner,
	}
	if issuer != nil {
		AppConfig.WorkloadIdentityFederation.Issuer = WifIssuerKeycloak
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

func wifTokenSecretNames(t *testing.T, client *KubernetesClient) []string {
	t.Helper()
	secrets, err := client.clientset.CoreV1().Secrets("meshstack").List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	var names []string
	for _, secret := range secrets.Items {
		if strings.HasPrefix(secret.Name, "wif-token-") {
			names = append(names, secret.Name)
		}
	}
	return names
}

func mountedWifTokenSecret(t *testing.T, client *KubernetesClient, pod *corev1.PodSpec) *corev1.Secret {
	t.Helper()
	var volume *corev1.Volume
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == wifTokenVolumeName {
			volume = &pod.Volumes[i]
		}
	}
	require.NotNil(t, volume)
	require.NotNil(t, volume.Secret)
	assert.Nil(t, volume.Secret.Optional)
	assert.Contains(t, pod.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      wifTokenVolumeName,
		MountPath: "/var/run/secrets/workload-identity/meshstack",
		ReadOnly:  true,
	})
	secret, err := client.clientset.CoreV1().Secrets("meshstack").Get(context.Background(), volume.Secret.SecretName, metav1.GetOptions{})
	require.NoError(t, err)
	return secret
}

func Test_TerraformRun_TokenOfBbdSubjectInJobSecret(t *testing.T) {
	issuer := &fakeWifTokenIssuer{}
	client, warner, _ := newTestKubernetesClient(t, issuer)

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Equal(t, []string{"bbd:platform-team:0d43a2b8-bbd"}, issuer.subjects)
	secret := mountedWifTokenSecret(t, client, pod)
	assert.True(t, strings.HasPrefix(secret.Name, "wif-token-"+testRunInfo.Uuid+"-"))
	assert.Equal(t, testWifToken, string(secret.Data["token"]))
	require.Len(t, secret.OwnerReferences, 1)
	assert.Equal(t, "runner-"+testRunInfo.Uuid, secret.OwnerReferences[0].Name)
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

	assert.Empty(t, wifTokenSecretNames(t, client))
	assert.Equal(t, []string{"run-json"}, volumeNames(pod))
	assert.Len(t, pod.Containers[0].VolumeMounts, 1)
	assert.Equal(t, []string{testRunInfo.Uuid}, warner.runIds)
}

func Test_CreatingTokenSecretFails_RunStartsWithoutTokenAndOwnerIsWarned(t *testing.T) {
	client, warner, _ := newTestKubernetesClient(t, &fakeWifTokenIssuer{})
	client.clientset.(*fake.Clientset).PrependReactor("create", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		secret := action.(k8stesting.CreateAction).GetObject().(*corev1.Secret)
		if strings.HasPrefix(secret.GenerateName, "wif-token-") {
			return true, nil, errors.New("API server unavailable")
		}
		return false, nil, nil
	})

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Equal(t, []string{"run-json"}, volumeNames(pod))
	assert.Equal(t, []string{testRunInfo.Uuid}, warner.runIds)
}

func Test_TokenSecretOfEarlierDispatch_NotMounted(t *testing.T) {
	client, _, _ := newTestKubernetesClient(t, &fakeWifTokenIssuer{})
	_, err := client.clientset.CoreV1().Secrets("meshstack").Create(context.Background(), &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "wif-token-" + testRunInfo.Uuid + "-earlier", Namespace: "meshstack"},
		Data:       map[string][]byte{"token": []byte("earlier-token")},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	pod := createTestJob(t, client, "TERRAFORM")

	secret := mountedWifTokenSecret(t, client, pod)
	assert.NotEqual(t, "wif-token-"+testRunInfo.Uuid+"-earlier", secret.Name)
	assert.Equal(t, testWifToken, string(secret.Data["token"]))
}

func Test_GitHubRun_NoTokenIssued(t *testing.T) {
	issuer := &fakeWifTokenIssuer{}
	client, warner, _ := newTestKubernetesClient(t, issuer)

	createTestJob(t, client, "GITHUB_WORKFLOW")

	assert.Empty(t, issuer.subjects)
	assert.Empty(t, warner.runIds)
}

func volumeNames(pod *corev1.PodSpec) []string {
	var names []string
	for _, volume := range pod.Volumes {
		names = append(names, volume.Name)
	}
	return names
}

func Test_KeycloakIssuer_OnlyTheMeshstackToken(t *testing.T) {
	client, _, _ := newTestKubernetesClient(t, &fakeWifTokenIssuer{})

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Equal(t, []string{"run-json", wifTokenVolumeName}, volumeNames(pod))
}

func Test_KubernetesIssuer_ProjectedTokensWithoutMeshstackToken(t *testing.T) {
	client, warner, _ := newTestKubernetesClient(t, nil)

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Empty(t, wifTokenSecretNames(t, client))
	assert.Equal(t, []string{"run-json", "service-account-token-azure", "service-account-token-gcp", "service-account-token-aws"}, volumeNames(pod))
	assert.Empty(t, warner.runIds)
}

func Test_OffIssuer_NoWorkloadIdentityToken(t *testing.T) {
	client, _, _ := newTestKubernetesClient(t, nil)
	AppConfig.WorkloadIdentityFederation.Issuer = WifIssuerOff

	pod := createTestJob(t, client, "TERRAFORM")

	assert.Equal(t, []string{"run-json"}, volumeNames(pod))
	assert.Len(t, pod.Containers[0].VolumeMounts, 1)
}
