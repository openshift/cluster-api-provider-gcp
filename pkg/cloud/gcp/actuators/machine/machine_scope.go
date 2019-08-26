package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	apicorev1 "k8s.io/api/core/v1"
)

// machineScopeParams defines the input parameters used to create a new MachineScope.
type machineScopeParams struct {
	machine           *machinev1.Machine
	credentialsSecret *apicorev1.Secret
}

// machineScope defines a scope defined around a machine and its cluster.
type machineScope struct {
	projectID      string
	computeService computeservice.GCPComputeService
	machine        *machinev1.Machine
	providerSpec   *v1beta1.GCPMachineProviderSpec
}

// newMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func newMachineScope(params machineScopeParams) (*machineScope, error) {
	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(params.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %v", err)
	}

	var serviceAccountJSON string
	if params.credentialsSecret != nil {
		data, exists := params.credentialsSecret.Data[credentialsSecretKey]
		if exists {
			serviceAccountJSON = string(data)
		}
	}

	projectID := providerSpec.ProjectID
	if len(projectID) == 0 {
		projectID, err = getProjectIDFromJSONKey([]byte(serviceAccountJSON))
		if err != nil {
			return nil, fmt.Errorf("error getting project from JSON key: %v", err)
		}
	}

	oauthClient, err := createOauth2Client(serviceAccountJSON, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("error creating oauth client: %v", err)
	}

	computeService, err := computeservice.NewComputeService(oauthClient)
	if err != nil {
		return nil, fmt.Errorf("error creating compute service: %v", err)
	}
	return &machineScope{
		projectID:      projectID,
		computeService: computeService,
		machine:        params.machine,
		providerSpec:   providerSpec,
	}, nil
}

// Close the MachineScope by persisting the machine spec, machine status after reconciling.
func (s *machineScope) Close() error {
	return nil
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
