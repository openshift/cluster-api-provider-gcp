package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machineapierros "github.com/openshift/machine-api-operator/pkg/controller/machine"
	machineclient "github.com/openshift/machine-api-operator/pkg/generated/clientset/versioned/typed/machine/v1beta1"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	apicorev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	credentialsSecretKey = "service_account.json"
)

// machineScopeParams defines the input parameters used to create a new MachineScope.
type machineScopeParams struct {
	machineClient machineclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
	machine       *machinev1.Machine
}

// MachineScope defines a scope defined around a machine and its cluster.
type MachineScope struct {
	machineClient  machineclient.MachineInterface
	coreClient     controllerclient.Client
	projectID      string
	providerID     string
	computeService computeservice.GCPComputeService
	machine        *machinev1.Machine
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus

	// origMachine captures original value of machine before it is updated (to
	// skip object updated if nothing is changed)
	origMachine *machinev1.Machine
	// origProviderStatus captures original value of machine provider status
	// before it is updated (to skip object updated if nothing is changed)
	origProviderStatus *v1beta1.GCPMachineProviderStatus
}

// NewMachineScope creates a new MachineScope from the supplied parameters.
// This is meant to be called for each machine actuator operation.
func NewMachineScope(params machineScopeParams) (*MachineScope, error) {
	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(params.machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("failed to get machine config: %v", err)
	}

	providerStatus, err := v1beta1.ProviderStatusFromRawExtension(params.machine.Status.ProviderStatus)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("failed to get machine provider status: %v", err.Error())
	}

	serviceAccountJSON, err := getCredentialsSecret(params.coreClient, *params.machine, *providerSpec)
	if err != nil {
		return nil, err
	}

	projectID := providerSpec.ProjectID
	if len(projectID) == 0 {
		projectID, err = getProjectIDFromJSONKey([]byte(serviceAccountJSON))
		if err != nil {
			return nil, machineapierros.InvalidMachineConfiguration("error getting project from JSON key: %v", err)
		}
	}

	oauthClient, err := createOauth2Client(serviceAccountJSON, compute.CloudPlatformScope)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("error creating oauth client: %v", err)
	}

	computeService, err := computeservice.NewComputeService(oauthClient)
	if err != nil {
		return nil, machineapierros.InvalidMachineConfiguration("error creating compute service: %v", err)
	}
	return &MachineScope{
		machineClient: params.machineClient.Machines(params.machine.Namespace),
		coreClient:    params.coreClient,
		projectID:     projectID,
		// https://github.com/kubernetes/kubernetes/blob/8765fa2e48974e005ad16e65cb5c3acf5acff17b/staging/src/k8s.io/legacy-cloud-providers/gce/gce_util.go#L204
		providerID:     fmt.Sprintf("gce://%s/%s/%s", projectID, providerSpec.Zone, params.machine.Name),
		computeService: computeService,
		// Deep copy the machine since it is changed outside
		// of the machine scope by consumers of the machine
		// scope (e.g. reconciler).
		machine:        params.machine.DeepCopy(),
		providerSpec:   providerSpec,
		providerStatus: providerStatus,
		// Once set, they can not be changed. Otherwise, status change computation
		// might be invalid and result in skipping the status update.
		origMachine:        params.machine.DeepCopy(),
		origProviderStatus: providerStatus.DeepCopy(),
	}, nil
}

// Close the MachineScope by persisting the machine spec, machine status after reconciling.
func (s *MachineScope) Close() error {
	if s.machineClient == nil {
		return errors.New("No machineClient is set for this scope")
	}

	// The machine status needs to be updated first since
	// the next call to storeMachineSpec updates entire machine
	// object. If done in the reverse order, the machine status
	// could be updated without setting the LastUpdated field
	// in the machine status. The following might occur:
	// 1. machine object is updated (including its status)
	// 2. the machine object is updated by different component/user meantime
	// 3. storeMachineStatus is called but fails since the machine object
	//    is outdated. The operation is reconciled but given the status
	//    was already set in the previous call, the status is no longer updated
	//    since the status updated condition is already false. Thus,
	//    the LastUpdated is not set/updated properly.
	if err := s.storeMachineStatus(); err != nil {
		return fmt.Errorf("[machinescope] failed to store provider status for machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}

	if err := s.storeMachineSpec(); err != nil {
		return fmt.Errorf("[machinescope] failed to update machine %q in namespace %q: %v", s.machine.Name, s.machine.Namespace, err)
	}

	return nil
}

func (s *MachineScope) storeMachineSpec() error {
	ext, err := v1beta1.RawExtensionFromProviderSpec(s.providerSpec)
	if err != nil {
		return err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	s.machine.Spec.ProviderSpec.Value = ext
	latestMachine, err := s.machineClient.Update(s.machine)
	if err != nil {
		return err
	}
	s.machine = latestMachine
	return nil
}

func (s *MachineScope) storeMachineStatus() error {
	if equality.Semantic.DeepEqual(s.providerStatus, s.origProviderStatus) && equality.Semantic.DeepEqual(s.machine.Status.Addresses, s.origMachine.Status.Addresses) {
		klog.Infof("%s: status unchanged", s.machine.Name)
		return nil
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", s.machine.Name, s.machine.ResourceVersion, s.machine.Generation)
	ext, err := v1beta1.RawExtensionFromProviderStatus(s.providerStatus)
	if err != nil {
		return err
	}

	s.machine.Status.ProviderStatus = ext
	time := metav1.Now()
	s.machine.Status.LastUpdated = &time
	latestMachine, err := s.machineClient.UpdateStatus(s.machine)
	if err != nil {
		return err
	}
	s.machine = latestMachine
	return nil
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
		if apimachineryerrors.IsNotFound(err) {
			machineapierros.InvalidMachineConfiguration("credentials secret %q in namespace %q not found: %v", spec.CredentialsSecret.Name, machine.GetNamespace(), err.Error())
		}
		return "", fmt.Errorf("error getting credentials secret %q in namespace %q: %v", spec.CredentialsSecret.Name, machine.GetNamespace(), err)
	}
	data, exists := credentialsSecret.Data[credentialsSecretKey]
	if !exists {
		return "", machineapierros.InvalidMachineConfiguration("secret %v/%v does not have %q field set. Thus, no credentials applied when creating an instance", machine.GetNamespace(), spec.CredentialsSecret.Name, credentialsSecretKey)
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
