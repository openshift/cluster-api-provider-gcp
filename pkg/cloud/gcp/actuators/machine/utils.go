package machine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	corev1 "k8s.io/api/core/v1"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// This expects the https://github.com/openshift/cloud-credential-operator to make a secret
// with a serviceAccount JSON Key content available. E.g:
//
//apiVersion: v1
//kind: Secret
//metadata:
//  name: gcp-sa
//  namespace: openshift-machine-api
//type: Opaque
//data:
//  serviceAccountJSON: base64 encoded content of the file
func getCredentialsSecret(coreClient controllerclient.Client, machine machinev1.Machine, spec v1beta1.GCPMachineProviderSpec) (string, error) {
	if spec.CredentialsSecret == nil {
		return "", nil
	}
	var credentialsSecret corev1.Secret

	if err := coreClient.Get(context.Background(), controllerclient.ObjectKey{Namespace: machine.GetNamespace(), Name: spec.CredentialsSecret.Name}, &credentialsSecret); err != nil {
		return "", fmt.Errorf("error getting credentials secret %q in namespace %q: %v", spec.CredentialsSecret.Name, machine.GetNamespace(), err)
	}
	data, exists := credentialsSecret.Data[credentialsSecretKey]
	if !exists {
		return "", fmt.Errorf("secret %v/%v does not have %q field set. Thus, no credentials applied when creating an instance", machine.GetNamespace(), spec.CredentialsSecret.Name, credentialsSecretKey)
	}

	return string(data), nil
}

func getProjectIDFromJSONKey(content []byte) (string, error) {
	var JSONKey struct {
		ProjectID string `json:"project_id"`
	}
	if err := json.Unmarshal(content, &JSONKey); err != nil {
		return "", fmt.Errorf("error un marshalling JSON key: %v", err)
	}
	return JSONKey.ProjectID, nil
}

func createOauth2Client(serviceAccountJSON string, scope ...string) (*http.Client, error) {
	ctx := context.Background()

	jwt, err := google.JWTConfigFromJSON([]byte(serviceAccountJSON), scope...)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, jwt.TokenSource(ctx)), nil
}

func newMachineContext(client controllerclient.Client, machine *machinev1.Machine) (*machineContext, error) {
	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %v", err)
	}

	providerStatus, err := v1beta1.ProviderStatusFromRawExtension(machine.Status.ProviderStatus)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get machine provider status")
	}

	serviceAccountJSON, err := getCredentialsSecret(client, *machine, *providerSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to get serviceAccountJSON: %v", err)
	}

	projectID, err := getProjectIDFromJSONKey([]byte(serviceAccountJSON))
	if err != nil {
		return nil, fmt.Errorf("error getting project from JSON key: %v", err)
	}

	oauthClient, err := createOauth2Client(serviceAccountJSON, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("error creating oauth client: %v", err)
	}

	computeService, err := computeservice.NewComputeService(oauthClient)
	if err != nil {
		return nil, fmt.Errorf("error creating compute service: %v", err)
	}

	machineCtx := &machineContext{
		projectID: projectID,
		// https://github.com/kubernetes/kubernetes/blob/8765fa2e48974e005ad16e65cb5c3acf5acff17b/staging/src/k8s.io/legacy-cloud-providers/gce/gce_util.go#L204
		providerID:     fmt.Sprintf("gce://%s/%s/%s", projectID, providerSpec.Zone, machine.Name),
		computeService: computeService,
		providerSpec:   providerSpec,
		providerStatus: providerStatus,
	}

	if providerSpec.UserDataSecret == nil {
		return machineCtx, nil
	}

	var userDataSecret corev1.Secret

	key := controllerclient.ObjectKey{
		Namespace: machine.GetNamespace(),
		Name:      providerSpec.UserDataSecret.Name,
	}

	if err := client.Get(context.Background(), key, &userDataSecret); err != nil {
		return nil, fmt.Errorf("error getting user data secret %q in namespace %q: %v", providerSpec.UserDataSecret.Name, machine.GetNamespace(), err)
	}

	data, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return nil, fmt.Errorf("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", machine.GetNamespace(), providerSpec.UserDataSecret.Name, userDataSecretKey)
	}

	machineCtx.userdata = base64.StdEncoding.EncodeToString(data)
	return machineCtx, nil
}
