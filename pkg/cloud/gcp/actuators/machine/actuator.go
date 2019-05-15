package machine

// This is a thin layer to implement the machine actuator interface with cloud provider details.
// The lifetime of scope and reconciler is a machine actuator operation.
// when scope is closed, it will persist to etcd the given machine spec and machine status (if modified)
import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	clusterv1 "github.com/openshift/cluster-api/pkg/apis/cluster/v1alpha1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	mapiclient "github.com/openshift/cluster-api/pkg/client/clientset_generated/clientset/typed/machine/v1beta1"
	"github.com/pkg/errors"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	compute "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	controllerclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	credentialsSecretKey = "serviceAccountJSON"
	operationRetryWait   = 5 * time.Second
	operationTimeOut     = 180 * time.Second
	userDataSecretKey    = "userData"
)

// Actuator is responsible for performing machine reconciliation.
type Actuator struct {
	machineClient mapiclient.MachineV1beta1Interface
	coreClient    controllerclient.Client
}

// ActuatorParams holds parameter information for Actuator.
type ActuatorParams struct {
	MachineClient mapiclient.MachineV1beta1Interface
	CoreClient    controllerclient.Client
}

type machineContext struct {
	computeService computeservice.GCPComputeService
	projectID      string
	providerID     string
	providerSpec   *v1beta1.GCPMachineProviderSpec
	providerStatus *v1beta1.GCPMachineProviderStatus
	userdata       string
}

// NewActuator returns an actuator.
func NewActuator(params ActuatorParams) *Actuator {
	return &Actuator{
		machineClient: params.MachineClient,
		coreClient:    params.CoreClient,
	}
}

// Create creates a machine and is invoked by the machine controller.
func (a *Actuator) Create(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Creating machine %q", machine.Name)

	machineCtx, err := a.machineContext(machine)
	if err != nil {
		return err
	}

	instance := &compute.Instance{
		CanIpForward:       machineCtx.providerSpec.CanIPForward,
		DeletionProtection: machineCtx.providerSpec.DeletionProtection,
		Labels:             machineCtx.providerSpec.Labels,
		MachineType:        fmt.Sprintf("zones/%s/machineTypes/%s", machineCtx.providerSpec.Zone, machineCtx.providerSpec.MachineType),
		Name:               machine.Name,
		Tags: &compute.Tags{
			Items: machineCtx.providerSpec.Tags,
		},
	}

	var disks = []*compute.AttachedDisk{}
	for _, disk := range machineCtx.providerSpec.Disks {
		disks = append(disks, &compute.AttachedDisk{
			AutoDelete: disk.AutoDelete,
			Boot:       disk.Boot,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				DiskSizeGb:  disk.SizeGb,
				DiskType:    fmt.Sprintf("zones/%s/diskTypes/%s", machineCtx.providerSpec.Zone, disk.Type),
				Labels:      disk.Labels,
				SourceImage: disk.Image,
			},
		})
	}
	instance.Disks = disks

	var networkInterfaces = []*compute.NetworkInterface{}
	for _, nic := range machineCtx.providerSpec.NetworkInterfaces {
		computeNIC := &compute.NetworkInterface{
			AccessConfigs: []*compute.AccessConfig{{}},
		}
		if len(nic.Network) != 0 {
			computeNIC.Network = fmt.Sprintf("projects/%s/global/networks/%s", machineCtx.projectID, nic.Network)
		}
		if len(nic.Subnetwork) != 0 {
			computeNIC.Subnetwork = fmt.Sprintf("regions/%s/subnetworks/%s", machineCtx.providerSpec.Region, nic.Subnetwork)
		}
		networkInterfaces = append(networkInterfaces, computeNIC)
	}
	instance.NetworkInterfaces = networkInterfaces

	var serviceAccounts = []*compute.ServiceAccount{}
	for _, sa := range machineCtx.providerSpec.ServiceAccounts {
		serviceAccounts = append(serviceAccounts, &compute.ServiceAccount{
			Email:  sa.Email,
			Scopes: sa.Scopes,
		})
	}
	instance.ServiceAccounts = serviceAccounts

	var metadataItems = []*compute.MetadataItems{{
		Key:   "user-data",
		Value: &machineCtx.userdata,
	}}

	for _, metadata := range machineCtx.providerSpec.Metadata {
		metadataItems = append(metadataItems, &compute.MetadataItems{
			Key:   metadata.Key,
			Value: metadata.Value,
		})
	}

	instance.Metadata = &compute.Metadata{
		Items: metadataItems,
	}

	operation, err := machineCtx.computeService.InstancesInsert(machineCtx.projectID, machineCtx.providerSpec.Zone, instance)
	if err != nil {
		machineCtx.providerStatus.Conditions = reconcileProviderConditions(machineCtx.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Reason:  machineCreationFailedReason,
			Message: err.Error(),
			Status:  corev1.ConditionFalse,
		})
		if _, err := a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
			return err
		}
		return fmt.Errorf("failed to create instance via compute service: %v", err)
	}

	if op, err := waitUntilOperationCompleted(machineCtx, operation.Name); err != nil {
		machineCtx.providerStatus.Conditions = reconcileProviderConditions(machineCtx.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Reason:  machineCreationFailedReason,
			Message: err.Error(),
			Status:  corev1.ConditionFalse,
		})
		if _, err := a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
			return err
		}
		return fmt.Errorf("failed to wait for create operation via compute service. Operation status: %v. Error: %v", op, err)
	}

	if _, err = a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
		return err
	}

	_, err = a.updateMachineSpec(machine, machineCtx.providerSpec)
	return err
}

func (a *Actuator) Exists(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) (bool, error) {
	klog.Infof("Checking if machine %q exists", machine.Name)

	machineCtx, err := a.machineContext(machine)
	if err != nil {
		return false, err
	}

	if err := a.validateProviderSpec(machineCtx.providerSpec); err != nil {
		return false, fmt.Errorf("failed validating machine provider spec: %v", err)
	}

	// Need to verify that our project/zone exists before checking
	// machine, as invalid project/zone produces same 404 error as
	// no machine.
	if err := validateZone(machineCtx); err != nil {
		return false, fmt.Errorf("unable to verify project/zone exists: %v/%v; err: %v", machineCtx.projectID, machineCtx.providerSpec.Zone, err)
	}

	_, err = machineCtx.computeService.InstancesGet(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name)
	if err == nil {
		klog.Infof("Machine %q already exists", machine.Name)
		return true, nil
	}

	if isNotFoundError(err) {
		klog.Infof("Machine %q does not exist", machine.Name)
		return false, nil
	}

	return false, fmt.Errorf("error getting running instances: %v", err)
}

func (a *Actuator) Update(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Updating machine %q", machine.Name)

	machineCtx, err := a.machineContext(machine)
	if err != nil {
		return err
	}

	if err := a.refreshMachineFromCloudState(machine, machineCtx); err != nil {
		return err
	}

	if _, err = a.updateMachineStatus(machine, machineCtx.providerStatus); err != nil {
		return err
	}

	_, err = a.updateMachineSpec(machine, machineCtx.providerSpec)
	return err

}

func (a *Actuator) Delete(ctx context.Context, cluster *clusterv1.Cluster, machine *machinev1.Machine) error {
	klog.Infof("Deleting machine %v", machine.Name)

	machineCtx, err := a.machineContext(machine)
	if err != nil {
		return err
	}

	exists, err := a.Exists(ctx, cluster, machine)
	if err != nil {
		return err
	}
	if !exists {
		klog.Infof("Machine %v not found during delete, skipping", machine.Name)
		return nil
	}

	operation, err := machineCtx.computeService.InstancesDelete(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name)
	if err != nil {
		return fmt.Errorf("failed to delete instance via compute service: %v", err)
	}

	if op, err := waitUntilOperationCompleted(machineCtx, operation.Name); err != nil {
		return fmt.Errorf("failed to wait for delete operation via compute service. Operation status: %v. Error: %v", op, err)
	}

	return nil
}

func waitUntilOperationCompleted(machineCtx *machineContext, operationName string) (*compute.Operation, error) {
	var op *compute.Operation
	var err error
	return op, wait.Poll(operationRetryWait, operationTimeOut, func() (bool, error) {
		op, err = machineCtx.computeService.ZoneOperationsGet(machineCtx.projectID, machineCtx.providerSpec.Zone, operationName)
		if err != nil {
			return false, err
		}
		klog.V(3).Infof("Waiting for %q operation to be completed... (status: %s)", op.OperationType, op.Status)
		if op.Status == "DONE" {
			if op.Error == nil {
				return true, nil
			}
			var err []error
			for _, opErr := range op.Error.Errors {
				err = append(err, fmt.Errorf("%s", *opErr))
			}
			return false, fmt.Errorf("the following errors occurred: %+v", err)
		}
		return false, nil
	})
}

func validateZone(machineCtx *machineContext) error {
	_, err := machineCtx.computeService.ZonesGet(machineCtx.projectID, machineCtx.providerSpec.Zone)
	return err
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

func (a *Actuator) machineContext(machine *machinev1.Machine) (*machineContext, error) {
	providerSpec, err := v1beta1.ProviderSpecFromRawExtension(machine.Spec.ProviderSpec.Value)
	if err != nil {
		return nil, fmt.Errorf("failed to get machine config: %v", err)
	}

	providerStatus, err := v1beta1.ProviderStatusFromRawExtension(machine.Status.ProviderStatus)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get machine provider status")
	}

	serviceAccountJSON, err := getCredentialsSecret(a.coreClient, *machine, *providerSpec)
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

	if err := a.coreClient.Get(context.Background(), key, &userDataSecret); err != nil {
		return nil, fmt.Errorf("error getting user data secret %q in namespace %q: %v", providerSpec.UserDataSecret.Name, machine.GetNamespace(), err)
	}

	data, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return nil, fmt.Errorf("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", machine.GetNamespace(), providerSpec.UserDataSecret.Name, userDataSecretKey)
	}

	machineCtx.userdata = base64.StdEncoding.EncodeToString(data)
	return machineCtx, nil
}

func (a *Actuator) updateMachineStatus(machine *machinev1.Machine, providerStatus *v1beta1.GCPMachineProviderStatus) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderStatus(providerStatus)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine status for %q, resourceVersion: %v, generation: %v", machine.Name, machine.ResourceVersion, machine.Generation)
	machine.Status.DeepCopyInto(&machine.Status)
	machine.Status.ProviderStatus = ext
	return a.machineClient.Machines(machine.Namespace).UpdateStatus(machine)
}

func (a *Actuator) updateMachineSpec(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (*machinev1.Machine, error) {
	ext, err := v1beta1.RawExtensionFromProviderSpec(providerSpec)
	if err != nil {
		return nil, err
	}

	klog.V(4).Infof("Storing machine spec for %q, resourceVersion: %v, generation: %v", machine.Name, machine.ResourceVersion, machine.Generation)
	machine.Spec.ProviderSpec.Value = ext
	return a.machineClient.Machines(machine.Namespace).Update(machine)
}

func (a *Actuator) refreshMachineFromCloudState(machine *machinev1.Machine, machineCtx *machineContext) error {
	klog.Infof("Reconciling machine object %q with cloud state", machine.Name)

	freshInstance, err := machineCtx.computeService.InstancesGet(machineCtx.projectID, machineCtx.providerSpec.Zone, machine.Name)
	if err != nil {
		return fmt.Errorf("failed to get instance via compute service: %v", err)
	}

	if len(freshInstance.NetworkInterfaces) < 1 {
		return fmt.Errorf("could not find network interfaces for instance %q", freshInstance.Name)
	}
	networkInterface := freshInstance.NetworkInterfaces[0]
	nodeAddresses := []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: networkInterface.NetworkIP}}
	for _, config := range networkInterface.AccessConfigs {
		nodeAddresses = append(nodeAddresses, corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: config.NatIP})
	}

	machine.Spec.ProviderID = &machineCtx.providerID
	machine.Status.Addresses = nodeAddresses

	machineCtx.providerStatus.InstanceState = &freshInstance.Status
	machineCtx.providerStatus.InstanceID = &freshInstance.Name
	machineCtx.providerStatus.Conditions = reconcileProviderConditions(machineCtx.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
		Type:    v1beta1.MachineCreated,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
		Status:  corev1.ConditionTrue,
	})

	return nil
}

func (a *Actuator) validateProviderSpec(providerSpec *v1beta1.GCPMachineProviderSpec) error {
	// TODO(alberto): First validation should happen via webhook
	// before the object is persisted. This is a complementary
	// validation to fail early in case of lacking proper webhook
	// validation. Default values can also be set here.
	return nil
}

func isNotFoundError(err error) bool {
	switch t := err.(type) {
	case *googleapi.Error:
		return t.Code == 404
	default:
		return false
	}
}
