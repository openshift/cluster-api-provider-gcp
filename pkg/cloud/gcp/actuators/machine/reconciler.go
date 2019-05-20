package machine

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	clustererror "github.com/openshift/cluster-api/pkg/controller/error"
	"github.com/pkg/errors"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	apicorev1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	operationDone      = "DONE"
	operationRetryWait = 5 * time.Second
	operationTimeOut   = 180 * time.Second
	requeuePeriod      = 10 * time.Second
	userDataSecretKey  = "userData"
)

// Reconciler are list of services required by machine actuator, easy to create a fake
type Reconciler struct {
	*machineScope
}

// NewReconciler populates all the services based on input scope
func newReconciler(scope *machineScope) *Reconciler {
	return &Reconciler{
		scope,
	}
}

func (r *Reconciler) newInstanceFromMachineScope() (*compute.Instance, error) {
	if err := validateMachine(*r.machine, *r.providerSpec); err != nil {
		return nil, fmt.Errorf("failed validating machine provider spec: %v", err)
	}

	instance := &compute.Instance{
		CanIpForward:       r.providerSpec.CanIPForward,
		DeletionProtection: r.providerSpec.DeletionProtection,
		Labels:             r.providerSpec.Labels,
		MachineType:        fmt.Sprintf("zones/%s/machineTypes/%s", r.providerSpec.Zone, r.providerSpec.MachineType),
		Name:               r.machine.Name,
		Tags: &compute.Tags{
			Items: r.providerSpec.Tags,
		},
	}

	// disks
	var disks = []*compute.AttachedDisk{}
	for _, disk := range r.providerSpec.Disks {
		disks = append(disks, &compute.AttachedDisk{
			AutoDelete: disk.AutoDelete,
			Boot:       disk.Boot,
			InitializeParams: &compute.AttachedDiskInitializeParams{
				DiskSizeGb:  disk.SizeGb,
				DiskType:    fmt.Sprintf("zones/%s/diskTypes/%s", r.providerSpec.Zone, disk.Type),
				Labels:      disk.Labels,
				SourceImage: disk.Image,
			},
		})
	}
	instance.Disks = disks

	// networking
	var networkInterfaces = []*compute.NetworkInterface{}
	for _, nic := range r.providerSpec.NetworkInterfaces {
		computeNIC := &compute.NetworkInterface{
			AccessConfigs: []*compute.AccessConfig{{}},
		}
		if len(nic.Network) != 0 {
			computeNIC.Network = fmt.Sprintf("projects/%s/global/networks/%s", r.projectID, nic.Network)
		}
		if len(nic.Subnetwork) != 0 {
			computeNIC.Subnetwork = fmt.Sprintf("regions/%s/subnetworks/%s", r.providerSpec.Region, nic.Subnetwork)
		}
		networkInterfaces = append(networkInterfaces, computeNIC)
	}
	instance.NetworkInterfaces = networkInterfaces

	// serviceAccounts
	var serviceAccounts = []*compute.ServiceAccount{}
	for _, sa := range r.providerSpec.ServiceAccounts {
		serviceAccounts = append(serviceAccounts, &compute.ServiceAccount{
			Email:  sa.Email,
			Scopes: sa.Scopes,
		})
	}
	instance.ServiceAccounts = serviceAccounts

	// userData
	userData, err := r.getCustomUserData()
	if err != nil {
		return nil, fmt.Errorf("error getting custom user data: %v", err)
	}
	var metadataItems = []*compute.MetadataItems{
		{
			Key:   "user-data",
			Value: &userData,
		},
	}
	for _, metadata := range r.providerSpec.Metadata {
		metadataItems = append(metadataItems, &compute.MetadataItems{
			Key:   metadata.Key,
			Value: metadata.Value,
		})
	}
	instance.Metadata = &compute.Metadata{
		Items: metadataItems,
	}

	return instance, nil
}

// Create creates machine if and only if machine exists, handled by cluster-api
func (r *Reconciler) create() error {
	instance, err := r.newInstanceFromMachineScope()
	if err != nil {
		return err
	}

	requestId := string(r.machine.UID)[1:] + `c` // create
	operation, err := r.computeService.InstancesInsert(requestId, r.projectID, r.providerSpec.Zone, instance)
	if err != nil {
		if reconcileErr := r.reconcileMachineWithCloudState(&v1beta1.GCPMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Reason:  machineCreationFailedReason,
			Message: err.Error(),
			Status:  corev1.ConditionFalse,
		}); reconcileErr != nil {
			klog.Infof("Reconcile machine state failed for machine %q: %v", r.machine.Name, reconcileErr)
		}
		return err
	}

	klog.Infof("Create Operation #%v status=%q for machine %q", operation.Id, operation.Status, r.machine.Name)

	// Accumulate spec/status changes as we progress to RUNNING.
	if err := r.reconcileMachineWithCloudState(nil); err != nil {
		return err
	}

	if operation.Status != operationDone {
		klog.Infof("Create Operation #%v not %q for machine %q, returning an error to requeue", operation.Id, operationDone, r.machine.Name)
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	// If this condition is written successfully on scope.Close()
	// then the machine will exist as far as future calls to
	// Actuator.Exists() are concerned.
	r.providerStatus.Conditions = reconcileProviderConditions(r.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
		Type:    v1beta1.MachineCreated,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
		Status:  corev1.ConditionTrue,
	})

	return nil
}

func (r *Reconciler) update() error {
	return r.reconcileMachineWithCloudState(nil)
}

// reconcileMachineWithCloudState reconcile machineSpec and status with the latest cloud state
// if a failedCondition is passed it updates the providerStatus.Conditions and return
// otherwise it fetches the relevant cloud instance and reconcile the rest of the fields
func (r *Reconciler) reconcileMachineWithCloudState(failedCondition *v1beta1.GCPMachineProviderCondition) error {
	klog.Infof("Reconciling machine object %q with cloud state", r.machine.Name)
	if failedCondition != nil {
		r.providerStatus.Conditions = reconcileProviderConditions(r.providerStatus.Conditions, *failedCondition)
		return nil
	} else {
		freshInstance, err := r.computeService.InstancesGet(r.projectID, r.providerSpec.Zone, r.machine.Name)
		if err != nil {
			return fmt.Errorf("failed to get instance via compute service: %v", err)
		}

		if len(freshInstance.NetworkInterfaces) < 1 {
			return fmt.Errorf("could not find network interfaces for instance %q", freshInstance.Name)
		}
		networkInterface := freshInstance.NetworkInterfaces[0]

		nodeAddresses := []v1.NodeAddress{{Type: v1.NodeInternalIP, Address: networkInterface.NetworkIP}}
		for _, config := range networkInterface.AccessConfigs {
			nodeAddresses = append(nodeAddresses, v1.NodeAddress{Type: v1.NodeExternalIP, Address: config.NatIP})
		}

		r.machine.Spec.ProviderID = &r.providerID
		r.machine.Status.Addresses = nodeAddresses
		r.providerStatus.InstanceState = &freshInstance.Status
		r.providerStatus.InstanceID = &freshInstance.Name
	}
	return nil
}

func (r *Reconciler) getCustomUserData() (string, error) {
	if r.providerSpec.UserDataSecret == nil {
		return "", nil
	}
	var userDataSecret apicorev1.Secret

	if err := r.coreClient.Get(context.Background(), client.ObjectKey{Namespace: r.machine.GetNamespace(), Name: r.providerSpec.UserDataSecret.Name}, &userDataSecret); err != nil {
		return "", fmt.Errorf("error getting user data secret %q in namespace %q: %v", r.providerSpec.UserDataSecret.Name, r.machine.GetNamespace(), err)
	}
	data, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return "", fmt.Errorf("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", r.machine.GetNamespace(), r.providerSpec.UserDataSecret.Name, userDataSecretKey)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func (r *Reconciler) waitUntilOperationCompleted(zone, operationName string) (*compute.Operation, error) {
	var op *compute.Operation
	var err error
	return op, wait.Poll(operationRetryWait, operationTimeOut, func() (bool, error) {
		op, err = r.computeService.ZoneOperationsGet(r.projectID, zone, operationName)
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

func validateMachine(machine machinev1.Machine, providerSpec v1beta1.GCPMachineProviderSpec) error {
	// TODO (alberto): First validation should happen via webhook before the object is persisted.
	// This is a complementary validation to fail early in case of lacking proper webhook validation.
	// Default values can also be set here
	return nil
}

// Returns true if machine exists.
func (r *Reconciler) exists() (bool, error) {
	// MachineCreated condition is only set when create() succeeds.
	if findCondition(r.providerStatus.Conditions, v1beta1.MachineCreated) == nil {
		return false, nil
	}

	if err := validateMachine(*r.machine, *r.providerSpec); err != nil {
		return false, fmt.Errorf("failed validating machine provider spec: %v", err)
	}
	zone := r.providerSpec.Zone
	// Need to verify that our project/zone exists before checking machine, as
	// invalid project/zone produces same 404 error as no machine.
	if err := r.validateZone(); err != nil {
		return false, fmt.Errorf("unable to verify project/zone exists: %v/%v; err: %v", r.projectID, zone, err)
	}

	instance, err := r.computeService.InstancesGet(r.projectID, r.providerSpec.Zone, r.machine.Name)
	if err != nil {
		e, ok := err.(*googleapi.Error)
		if ok && e.Code == 404 {
			return false, nil
		}
		return false, errors.Wrapf(err, "InstanceGet failed for machine %q", r.machine.Name)
	}
	return instance != nil, nil
}

// Returns true if machine exists.
func (r *Reconciler) delete() error {
	exists, err := r.exists()
	if err != nil {
		return err
	}
	if !exists {
		klog.Infof("Machine %v not found during delete, skipping", r.machine.Name)
		return nil
	}
	zone := r.providerSpec.Zone
	operation, err := r.computeService.InstancesDelete(r.projectID, zone, r.machine.Name)
	if err != nil {
		return fmt.Errorf("failed to delete instance via compute service: %v", err)
	}
	if op, err := r.waitUntilOperationCompleted(zone, operation.Name); err != nil {
		return fmt.Errorf("failed to wait for delete operation via compute service. Operation status: %v. Error: %v", op, err)
	}
	return nil
}

func (r *Reconciler) validateZone() error {
	_, err := r.computeService.ZonesGet(r.projectID, r.providerSpec.Zone)
	return err
}
