package machine

import (
	"context"
	"fmt"
	"time"

	"strconv"
	"strings"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	machinev1 "github.com/openshift/machine-api-operator/pkg/apis/machine/v1beta1"
	machinecontroller "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"google.golang.org/api/compute/v1"
	googleapi "google.golang.org/api/googleapi"
	apicorev1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	apimachineryerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	userDataSecretKey   = "userData"
	requeueAfterSeconds = 20
	instanceLinkFmt     = "https://www.googleapis.com/compute/v1/projects/%s/zones/%s/instances/%s"
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

// Create creates machine if and only if machine exists, handled by cluster-api
func (r *Reconciler) create() error {
	if err := validateMachine(*r.machine, *r.providerSpec); err != nil {
		return machinecontroller.InvalidMachineConfiguration("failed validating machine provider spec: %v", err)
	}

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

	if instance.Labels == nil {
		instance.Labels = map[string]string{}
	}
	instance.Labels[fmt.Sprintf("kubernetes-io-cluster-%v", r.machine.Labels[machinev1.MachineClusterIDLabel])] = "owned"

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
				SourceImage: googleapi.ResolveRelative(r.computeService.BasePath(), fmt.Sprintf("%s/global/images/%s", r.projectID, disk.Image)),
			},
		})
	}
	instance.Disks = disks

	// networking
	var networkInterfaces = []*compute.NetworkInterface{}

	for _, nic := range r.providerSpec.NetworkInterfaces {
		accessConfigs := []*compute.AccessConfig{}
		if nic.PublicIP {
			accessConfigs = append(accessConfigs, &compute.AccessConfig{})
		}
		computeNIC := &compute.NetworkInterface{
			AccessConfigs: accessConfigs,
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
		return fmt.Errorf("error getting custom user data: %v", err)
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

	_, err = r.computeService.InstancesInsert(r.projectID, zone, instance)
	if err != nil {
		if reconcileWithCloudError := r.reconcileMachineWithCloudState(&v1beta1.GCPMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Reason:  machineCreationFailedReason,
			Message: err.Error(),
			Status:  corev1.ConditionFalse,
		}); reconcileWithCloudError != nil {
			klog.Errorf("Failed to reconcile machine with cloud state: %v", reconcileWithCloudError)
		}
		if googleError, ok := err.(*googleapi.Error); ok {
			// we return InvalidMachineConfiguration for 4xx errors which by convention signal client misconfiguration
			// https://tools.ietf.org/html/rfc2616#section-6.1.1
			if strings.HasPrefix(strconv.Itoa(googleError.Code), "4") {
				klog.Infof("Error launching instance: %v", googleError)
				return machinecontroller.InvalidMachineConfiguration("error launching instance: %v", googleError.Error())
			}
		}
		return fmt.Errorf("failed to create instance via compute service: %v", err)
	}
	return r.reconcileMachineWithCloudState(nil)
}

func (r *Reconciler) update() error {
	// Add target pools, if necessary
	if err := r.processTargetPools(true, r.addInstanceToTargetPool); err != nil {
		return err
	}
	return r.reconcileMachineWithCloudState(nil)
}

// reconcileMachineWithCloudState reconcile machineSpec and status with the latest cloud state
// if a failedCondition is passed it updates the providerStatus.Conditions and return
// otherwise it fetches the relevant cloud instance and reconcile the rest of the fields
func (r *Reconciler) reconcileMachineWithCloudState(failedCondition *v1beta1.GCPMachineProviderCondition) error {
	klog.Infof("%s: Reconciling machine object with cloud state", r.machine.Name)
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
			Type:    corev1.NodeInternalDNS,
			Address: fmt.Sprintf("%s.c.%s.internal", r.machine.Name, r.projectID),
		})
		// Add the machine's name as a known NodeInternalDNS because GCP platform
		// provides search paths to resolve those.
		// https://cloud.google.com/compute/docs/internal-dns#resolv.conf
		nodeAddresses = append(nodeAddresses, corev1.NodeAddress{
			Type:    corev1.NodeInternalDNS,
			Address: r.machine.GetName(),
		})

		r.machine.Spec.ProviderID = &r.providerID
		r.machine.Status.Addresses = nodeAddresses
		r.providerStatus.InstanceState = &freshInstance.Status
		r.providerStatus.InstanceID = &freshInstance.Name
		succeedCondition := v1beta1.GCPMachineProviderCondition{
			Type:    v1beta1.MachineCreated,
			Reason:  machineCreationSucceedReason,
			Message: machineCreationSucceedMessage,
			Status:  corev1.ConditionTrue,
		}
		r.providerStatus.Conditions = reconcileProviderConditions(r.providerStatus.Conditions, succeedCondition)

		r.setMachineCloudProviderSpecifics(freshInstance)

		if freshInstance.Status != "RUNNING" {
			klog.Infof("%s: machine status is %q, requeuing...", r.machine.Name, freshInstance.Status)
			return &machinecontroller.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
		}
	}

	return nil
}

func (r *Reconciler) setMachineCloudProviderSpecifics(instance *compute.Instance) {
	if r.machine.Labels == nil {
		r.machine.Labels = make(map[string]string)
	}

	if r.machine.Annotations == nil {
		r.machine.Annotations = make(map[string]string)
	}

	r.machine.Annotations[machinecontroller.MachineInstanceStateAnnotationName] = instance.Status
	// TODO(jchaloup): detect all three from instance rather than
	// always assuming it's the same as what is specified in the provider spec
	r.machine.Labels[machinecontroller.MachineInstanceTypeLabelName] = r.providerSpec.MachineType
	r.machine.Labels[machinecontroller.MachineRegionLabelName] = r.providerSpec.Region
	r.machine.Labels[machinecontroller.MachineAZLabelName] = r.providerSpec.Zone
}

func (r *Reconciler) getCustomUserData() (string, error) {
	if r.providerSpec.UserDataSecret == nil {
		return "", nil
	}
	var userDataSecret apicorev1.Secret

	if err := r.coreClient.Get(context.Background(), client.ObjectKey{Namespace: r.machine.GetNamespace(), Name: r.providerSpec.UserDataSecret.Name}, &userDataSecret); err != nil {
		if apimachineryerrors.IsNotFound(err) {
			return "", machinecontroller.InvalidMachineConfiguration("user data secret %q in namespace %q not found: %v", r.providerSpec.UserDataSecret.Name, r.machine.GetNamespace(), err)
		}
		return "", fmt.Errorf("error getting user data secret %q in namespace %q: %v", r.providerSpec.UserDataSecret.Name, r.machine.GetNamespace(), err)
	}
	data, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return "", machinecontroller.InvalidMachineConfiguration("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", r.machine.GetNamespace(), r.providerSpec.UserDataSecret.Name, userDataSecretKey)
	}
	return string(data), nil
}

func validateMachine(machine machinev1.Machine, providerSpec v1beta1.GCPMachineProviderSpec) error {
	// TODO (alberto): First validation should happen via webhook before the object is persisted.
	// This is a complementary validation to fail early in case of lacking proper webhook validation.
	// Default values can also be set here
	if providerSpec.TargetPools != nil {
		for _, pool := range providerSpec.TargetPools {
			if pool == "" {
				return machinecontroller.InvalidMachineConfiguration("all target pools must have valid name")
			}
		}
	}

	if machine.Labels[machinev1.MachineClusterIDLabel] == "" {
		return machinecontroller.InvalidMachineConfiguration("machine is missing %q label", machinev1.MachineClusterIDLabel)
	}

	return nil
}

// Returns true if machine exists.
func (r *Reconciler) exists() (bool, error) {
	if err := validateMachine(*r.machine, *r.providerSpec); err != nil {
		return false, fmt.Errorf("failed validating machine provider spec: %v", err)
	}
	zone := r.providerSpec.Zone
	// Need to verify that our project/zone exists before checking machine, as
	// invalid project/zone produces same 404 error as no machine.
	if err := r.validateZone(); err != nil {
		if isNotFoundError(err) {
			// this error type bubbles back up to the machine-controller to allow
			// us to delete machines that were never properly created due to
			// invalid zone.
			return false, machinecontroller.InvalidMachineConfiguration(fmt.Sprintf("%s: Machine does not exist", r.machine.Name))
		}
		return false, fmt.Errorf("unable to verify project/zone exists: %v/%v; err: %v", r.projectID, zone, err)
	}

	instance, err := r.computeService.InstancesGet(r.projectID, zone, r.machine.Name)
	if instance != nil && err == nil {
		return true, nil
	}
	if isNotFoundError(err) {
		klog.Infof("%s: Machine does not exist", r.machine.Name)
		return false, nil
	}
	return false, fmt.Errorf("error getting running instances: %v", err)
}

// Returns true if machine exists.
func (r *Reconciler) delete() error {
	// Remove instance from target pools, if necessary
	if err := r.processTargetPools(false, r.deleteInstanceFromTargetPool); err != nil {
		return err
	}
	exists, err := r.exists()
	if err != nil {
		return err
	}
	if !exists {
		klog.Infof("%s: Machine not found during delete, skipping", r.machine.Name)
		return nil
	}
	if _, err = r.computeService.InstancesDelete(string(r.machine.UID), r.projectID, r.providerSpec.Zone, r.machine.Name); err != nil {
		return fmt.Errorf("failed to delete instance via compute service: %v", err)
	}
	klog.Infof("%s: machine status is exists, requeuing...", r.machine.Name)
	return &machinecontroller.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
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

func fmtInstanceSelfLink(project, zone, name string) string {
	return fmt.Sprintf(instanceLinkFmt, project, zone, name)
}

func (r *Reconciler) instanceExistsInPool(instanceLink string, pool string) (bool, error) {
	// Get target pool
	tp, err := r.computeService.TargetPoolsGet(r.projectID, r.providerSpec.Region, pool)
	if err != nil {
		return false, fmt.Errorf("unable to get targetpool: %v", err)
	}

	for _, link := range tp.Instances {
		if instanceLink == link {
			return true, nil
		}
	}
	return false, nil
}

type poolProcessor func(instanceLink, pool string) error

func (r *Reconciler) processTargetPools(desired bool, poolFunc poolProcessor) error {
	instanceSelfLink := fmtInstanceSelfLink(r.projectID, r.providerSpec.Zone, r.machine.Name)
	// TargetPools may be empty/nil, and that's okay.
	for _, pool := range r.providerSpec.TargetPools {
		present, err := r.instanceExistsInPool(instanceSelfLink, pool)
		if err != nil {
			return err
		}
		if present != desired {
			klog.Infof("%v: reconciling instance for targetpool with cloud provider; desired state: %v", r.machine.Name, desired)
			err := poolFunc(instanceSelfLink, pool)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) addInstanceToTargetPool(instanceLink string, pool string) error {
	_, err := r.computeService.TargetPoolsAddInstance(r.projectID, r.providerSpec.Region, pool, instanceLink)
	// Probably safe to disregard the returned operation; it either worked or it didn't.
	// Even if the instance doesn't exist, it will return without error and the non-existent
	// instance will be associated.
	if err != nil {
		return fmt.Errorf("failed to add instance %v to target pool %v: %v", r.machine.Name, pool, err)
	}
	return nil
}

func (r *Reconciler) deleteInstanceFromTargetPool(instanceLink string, pool string) error {
	_, err := r.computeService.TargetPoolsRemoveInstance(r.projectID, r.providerSpec.Region, pool, instanceLink)
	if err != nil {
		return fmt.Errorf("failed to remove instance %v from target pool %v: %v", r.machine.Name, pool, err)
	}
	return nil
}
