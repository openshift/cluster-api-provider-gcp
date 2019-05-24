package machine

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	clustererror "github.com/openshift/cluster-api/pkg/controller/error"
	"google.golang.org/api/compute/v1"
	googleapi "google.golang.org/api/googleapi"
	apicorev1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	userDataSecretKey = "userData"
	pendingCreateKey  = "machine.openshift.io/cluster-api-provider-gcp-CREATE-ID"
	pendingDeleteKey  = "machine.openshift.io/cluster-api-provider-gcp-DELETE-ID"
	requeuePeriod     = 20 * time.Second
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

func (r *Reconciler) createInstanceFromMachineScope() (*compute.Instance, error) {
	zone := r.providerSpec.Zone
	instance := &compute.Instance{
		CanIpForward:       r.providerSpec.CanIPForward,
		DeletionProtection: r.providerSpec.DeletionProtection,
		Labels:             r.providerSpec.Labels,
		MachineType:        fmt.Sprintf("zones/%s/machineTypes/%s", zone, r.providerSpec.MachineType),
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
				DiskType:    fmt.Sprintf("zones/%s/diskTypes/%s", zone, disk.Type),
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

// Create creates a new cloud instance
func (r *Reconciler) create() error {
	if err := validateMachine(*r.machine, *r.providerSpec); err != nil {
		return fmt.Errorf("failed validating machine provider spec: %v", err)
	}

	if r.machine.Annotations == nil {
		r.machine.Annotations = map[string]string{}
	}

	instance, err := r.instanceGet()
	if err != nil {
		return err
	}

	var operation *compute.Operation

	if instance != nil && haveCreateOperationInProgress(r.machine) {
		operation, err = r.computeService.ZoneOperationsGet(r.projectID, r.providerSpec.Zone, r.machine.Annotations[pendingCreateKey])
		if err != nil {
			return err
		}
	} else {
		instance, err = r.createInstanceFromMachineScope()
		if err != nil {
			return err
		}
		operation, err = r.computeService.InstancesInsert(r.projectID, r.providerSpec.Zone, instance)
		if err != nil {
			r.providerStatus.Conditions = setProviderConditions(r.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
				Type:    v1beta1.MachineCreated,
				Reason:  machineCreationFailedReason,
				Message: err.Error(),
				Status:  corev1.ConditionFalse,
			})
			return fmt.Errorf("failed to create instance via compute service: %v", err)
		}
		r.machine.Annotations[pendingCreateKey] = fmt.Sprintf("%v", operation.Id)
	}

	klog.Infof("Instance create operation #%v status=%q for machine %q", operation.Id, operation.Status, r.machine.Name)
	if err = r.reconcileMachineWithCloudState(); err != nil {
		return err
	}

	if operation.Status != "DONE" {
		klog.Infof("Instance create operation #%v incomplete, returning an error to requeue", operation.Id)
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}

	delete(r.machine.Annotations, pendingCreateKey)
	r.providerStatus.Conditions = setProviderConditions(r.providerStatus.Conditions, v1beta1.GCPMachineProviderCondition{
		Type:    v1beta1.MachineCreated,
		Reason:  machineCreationSucceedReason,
		Message: machineCreationSucceedMessage,
		Status:  corev1.ConditionTrue,
	})

	_, err = r.persist()
	return err
}

func (r *Reconciler) update() error {
	if err := r.reconcileMachineWithCloudState(); err != nil {
		return err
	}
	_, err := r.persist()
	return err
}

// reconcileMachineWithCloudState reconcile machineSpec and status with the latest cloud state
// if a failedCondition is passed it updates the providerStatus.Conditions and return
// otherwise it fetches the relevant cloud instance and reconcile the rest of the fields
func (r *Reconciler) reconcileMachineWithCloudState() error {
	klog.Infof("Reconciling machine object %q with cloud state", r.machine.Name)
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
	// Since we don't know when the project was created, we must account for
	// both types of internal-dns:
	// https://cloud.google.com/compute/docs/internal-dns#instance-fully-qualified-domain-names
	// [INSTANCE_NAME].[ZONE].c.[PROJECT_ID].internal (newer)
	nodeAddresses = append(nodeAddresses, corev1.NodeAddress{
		Type:    corev1.NodeInternalDNS,
		Address: fmt.Sprintf("%s.%s.c.%s.internal", r.machine.Name, r.providerSpec.Zone, r.projectID),
	})
	// [INSTANCE_NAME].c.[PROJECT_ID].internal
	nodeAddresses = append(nodeAddresses, corev1.NodeAddress{
		Type:    "AltInternalDNS",
		Address: fmt.Sprintf("%s.c.%s.internal", r.machine.Name, r.projectID),
	})

	r.machine.Spec.ProviderID = &r.providerID
	r.machine.Status.Addresses = nodeAddresses
	r.providerStatus.InstanceState = &freshInstance.Status
	r.providerStatus.InstanceID = &freshInstance.Name
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

func validateMachine(machine machinev1.Machine, providerSpec v1beta1.GCPMachineProviderSpec) error {
	// TODO (alberto): First validation should happen via webhook before the object is persisted.
	// This is a complementary validation to fail early in case of lacking proper webhook validation.
	// Default values can also be set here
	return nil
}

// Returns true if machine exists.
func (r *Reconciler) exists() (bool, error) {
	instance, err := r.instanceGet()
	if err != nil {
		return false, err
	}
	return instance != nil && !haveCreateOperationInProgress(r.machine), nil
}

// delete deletes the cloud instance if it exists
func (r *Reconciler) delete() error {
	instance, err := r.instanceGet()
	if err != nil {
		return err
	}
	if instance == nil {
		klog.Infof("Machine %v not found during delete, skipping", r.machine.Name)
		return nil
	}

	if r.machine.Annotations == nil {
		r.machine.Annotations = map[string]string{}
	}

	var operation *compute.Operation

	if haveDeleteOperationInProgress(r.machine) {
		operation, err = r.computeService.ZoneOperationsGet(r.projectID, r.providerSpec.Zone, r.machine.Annotations[pendingDeleteKey])
		if err != nil {
			return fmt.Errorf("failed to get existing delete operation via compute service: %v", err)
		}
	} else {
		operation, err = r.computeService.InstancesDelete(r.projectID, r.providerSpec.Zone, r.machine.Name)
		if err != nil {
			return fmt.Errorf("failed to delete instance via compute service: %v", err)
		}
		r.machine.Annotations[pendingDeleteKey] = fmt.Sprintf("%v", operation.Id)
	}

	klog.Infof("Delete operation #%v status=%q for machine %q", operation.Id, operation.Status, r.machine.Name)
	if operation.Status != "DONE" {
		return &clustererror.RequeueAfterError{RequeueAfter: requeuePeriod}
	}
	delete(r.machine.Annotations, pendingDeleteKey)
	_, err = r.persist()
	return err
}

func (r *Reconciler) validateZone() error {
	_, err := r.computeService.ZonesGet(r.projectID, r.providerSpec.Zone)
	return err
}

func isNotFoundError(err error) bool {
	switch t := err.(type) {
	case *googleapi.Error:
		return t.Code == 404
	}
	return false
}

func haveCreateOperationInProgress(m *machinev1.Machine) bool {
	return m.Annotations[pendingCreateKey] != ""
}

func haveDeleteOperationInProgress(m *machinev1.Machine) bool {
	return m.Annotations[pendingDeleteKey] != ""
}
