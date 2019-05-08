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
	machineclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"
)

const (
	credentialsSecretKey = "serviceAccountJSON"
)

// machineScopeParams defines the input parameters used to create a new MachineScope.
type machineScopeParams struct {
	machineClient machineclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
	machine       *machinev1.Machine
}

// machineScope defines a scope defined around a machine and its cluster.
type machineScope struct {
	machineClient  machineclient.MachineInterface
	coreClient     controllerclient.Client
	projectID      string
	computeService computeservice.GCPComputeService
	machine        *machinev1.Machine
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus
}

// newMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func newMachineScope(params machineScopeParams) (*machineScope, error) {
	providerSpec, err := machineConfigFromProviderSpec(params.machine.Spec.ProviderSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %v", err)
	}

	serviceAccountJSON, err := getCredentialsSecret(params.coreClient, *params.machine, *providerSpec)
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
	return &machineScope{
		machineClient:  params.machineClient.Machines(params.machine.Namespace),
		coreClient:     params.coreClient,
		projectID:      projectID,
		computeService: computeService,
		machine:        params.machine,
		providerSpec:   providerSpec,
	}, nil
}

// Close the MachineScope by updating the machine spec, machine status.
func (m *machineScope) Close() {
	//TODO (alberto): implement this. Status can be refreshed here
}

// machineConfigFromProviderSpec tries to decode the JSON-encoded spec, falling back on getting a MachineClass if the value is absent.
func machineConfigFromProviderSpec(providerConfig machinev1.ProviderSpec) (*v1beta1.GCPMachineProviderSpec, error) {
	if providerConfig.Value == nil {
		return nil, fmt.Errorf("unable to find machine provider config: Spec.ProviderSpec.Value is not set")
	}
	return unmarshalProviderSpec(providerConfig.Value)
}

func unmarshalProviderSpec(spec *runtime.RawExtension) (*v1beta1.GCPMachineProviderSpec, error) {
	var config v1beta1.GCPMachineProviderSpec
	if spec != nil {
		if err := yaml.Unmarshal(spec.Raw, &config); err != nil {
			return nil, fmt.Errorf("error unmarshalling providerSpec: %v", err)
		}
	}
	klog.V(5).Infof("Found ProviderSpec: %+v", config)
	return &config, nil
}

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
	var credentialsSecret apicorev1.Secret

	if err := coreClient.Get(context.Background(), client.ObjectKey{Namespace: machine.GetNamespace(), Name: spec.CredentialsSecret.Name}, &credentialsSecret); err != nil {
		return "", fmt.Errorf("error getting user data secret %q in namespace %q: %v", spec.UserDataSecret.Name, machine.GetNamespace(), err)
	}
	data, exists := credentialsSecret.Data[credentialsSecretKey]
	if !exists {
		return "", fmt.Errorf("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", machine.GetNamespace(), spec.UserDataSecret.Name, credentialsSecretKey)
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
