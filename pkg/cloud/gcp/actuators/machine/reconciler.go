package machine

import (
	"context"
	"fmt"
	"time"

	"github.com/openshift/cluster-api-provider-gcp/pkg/apis/gcpprovider/v1beta1"
	machinev1 "github.com/openshift/cluster-api/pkg/apis/machine/v1beta1"
	clustererror "github.com/openshift/cluster-api/pkg/controller/error"
	"google.golang.org/api/compute/v1"
	googleapi "google.golang.org/api/googleapi"
	apicorev1 "k8s.io/api/core/v1"
	corev1 "k8s.io/api/core/v1"
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
func (r *Reconciler) create(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (*compute.Instance, error) {
	if err := validateMachine(*machine, *providerSpec); err != nil {
		return nil, fmt.Errorf("failed validating machine provider spec: %v", err)
	}

	zone := providerSpec.Zone
	instance := &compute.Instance{
		CanIpForward:       providerSpec.CanIPForward,
		DeletionProtection: providerSpec.DeletionProtection,
		Labels:             providerSpec.Labels,
		MachineType:        fmt.Sprintf("zones/%s/machineTypes/%s", zone, providerSpec.MachineType),
		Name:               machine.Name,
		Tags: &compute.Tags{
			Items: providerSpec.Tags,
		},
	}

	// disks
	var disks = []*compute.AttachedDisk{}
	for _, disk := range providerSpec.Disks {
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

	for _, nic := range providerSpec.NetworkInterfaces {
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
			computeNIC.Subnetwork = fmt.Sprintf("regions/%s/subnetworks/%s", providerSpec.Region, nic.Subnetwork)
		}
		networkInterfaces = append(networkInterfaces, computeNIC)
	}
	instance.NetworkInterfaces = networkInterfaces

	// serviceAccounts
	var serviceAccounts = []*compute.ServiceAccount{}
	for _, sa := range providerSpec.ServiceAccounts {
		serviceAccounts = append(serviceAccounts, &compute.ServiceAccount{
			Email:  sa.Email,
			Scopes: sa.Scopes,
		})
	}
	instance.ServiceAccounts = serviceAccounts

	// userData
	userData, err := r.getCustomUserData(machine, providerSpec)
	if err != nil {
		return nil, fmt.Errorf("error getting custom user data: %v", err)
	}
	var metadataItems = []*compute.MetadataItems{
		{
			Key:   "user-data",
			Value: &userData,
		},
	}
	for _, metadata := range providerSpec.Metadata {
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
		return nil, fmt.Errorf("failed to create instance via compute service: %v", err)
	}

	freshInstance, err := r.computeService.InstancesGet(r.projectID, providerSpec.Zone, machine.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance via compute service: %v", err)
	}

	return freshInstance, nil
}

func (r *Reconciler) update(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (*compute.Instance, error) {
	// Add target pools, if necessary
	if err := r.processTargetPools(true, providerSpec.TargetPools, r.addInstanceToTargetPool, providerSpec.Region, providerSpec.Zone, machine.Name); err != nil {
		return nil, err
	}

	freshInstance, err := r.computeService.InstancesGet(r.projectID, providerSpec.Zone, machine.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get instance via compute service: %v", err)
	}

	return freshInstance, nil
}

func (r *Reconciler) getCustomUserData(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (string, error) {
	if providerSpec.UserDataSecret == nil {
		return "", nil
	}
	var userDataSecret apicorev1.Secret

	if err := r.coreClient.Get(context.Background(), client.ObjectKey{Namespace: machine.GetNamespace(), Name: providerSpec.UserDataSecret.Name}, &userDataSecret); err != nil {
		return "", fmt.Errorf("error getting user data secret %q in namespace %q: %v", providerSpec.UserDataSecret.Name, machine.GetNamespace(), err)
	}
	data, exists := userDataSecret.Data[userDataSecretKey]
	if !exists {
		return "", fmt.Errorf("secret %v/%v does not have %q field set. Thus, no user data applied when creating an instance", machine.GetNamespace(), providerSpec.UserDataSecret.Name, userDataSecretKey)
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
				return fmt.Errorf("all target pools must have valid name")
			}
		}
	}
	return nil
}

// Returns true if machine exists.
func (r *Reconciler) exists(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) (bool, error) {
	if err := validateMachine(*machine, *providerSpec); err != nil {
		return false, fmt.Errorf("failed validating machine provider spec: %v", err)
	}
	zone := providerSpec.Zone
	// Need to verify that our project/zone exists before checking machine, as
	// invalid project/zone produces same 404 error as no machine.
	if err := r.validateZone(providerSpec.Zone); err != nil {
		return false, fmt.Errorf("unable to verify project/zone exists: %v/%v; err: %v", r.projectID, zone, err)
	}
	instance, err := r.computeService.InstancesGet(r.projectID, zone, machine.Name)
	if err == nil {
		switch instance.Status {
		case "TERMINATED":
			klog.Infof("Machine %q is considered as non existent as its status is %q", machine.Name, instance.Status)
			return false, nil
		default:
			klog.Infof("Machine %q already exists", machine.Name)
			return true, nil
		}
	}
	if isNotFoundError(err) {
		klog.Infof("%s: Machine does not exist", machine.Name)
		return false, nil
	}
	return false, fmt.Errorf("error getting running instances: %v", err)
}

// Returns true if machine exists.
func (r *Reconciler) delete(machine *machinev1.Machine, providerSpec *v1beta1.GCPMachineProviderSpec) error {
	// Remove instance from target pools, if necessary
	if err := r.processTargetPools(false, providerSpec.TargetPools, r.deleteInstanceFromTargetPool, providerSpec.Region, providerSpec.Zone, machine.Name); err != nil {
		return err
	}
	exists, err := r.exists(machine, providerSpec)
	if err != nil {
		return err
	}
	if !exists {
		klog.Infof("%s: Machine not found during delete, skipping", machine.Name)
		return nil
	}
	if _, err = r.computeService.InstancesDelete(string(machine.UID), r.projectID, providerSpec.Zone, machine.Name); err != nil {
		return fmt.Errorf("failed to delete instance via compute service: %v", err)
	}
	klog.Infof("%s: machine status is exists, requeuing...", machine.Name)
	return &clustererror.RequeueAfterError{RequeueAfter: requeueAfterSeconds * time.Second}
}

func (r *Reconciler) validateZone(zone string) error {
	_, err := r.computeService.ZonesGet(r.projectID, zone)
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

func (r *Reconciler) instanceExistsInPool(instanceLink string, pool, region string) (bool, error) {
	// Get target pool
	tp, err := r.computeService.TargetPoolsGet(r.projectID, region, pool)
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

type poolProcessor func(instanceLink, pool, zone, instanceName string) error

func (r *Reconciler) processTargetPools(desired bool, targetPools []string, poolFunc poolProcessor, region, zone, instanceName string) error {
	instanceSelfLink := fmtInstanceSelfLink(r.projectID, zone, instanceName)
	// TargetPools may be empty/nil, and that's okay.
	for _, pool := range targetPools {
		present, err := r.instanceExistsInPool(instanceSelfLink, pool, region)
		if err != nil {
			return err
		}
		if present != desired {
			klog.Infof("%v: reconciling instance for targetpool with cloud provider; desired state: %v", instanceName, desired)
			err := poolFunc(instanceSelfLink, pool, region, instanceName)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Reconciler) addInstanceToTargetPool(instanceLink string, pool string, region, instanceName string) error {
	_, err := r.computeService.TargetPoolsAddInstance(r.projectID, region, pool, instanceLink)
	// Probably safe to disregard the returned operation; it either worked or it didn't.
	// Even if the instance doesn't exist, it will return without error and the non-existent
	// instance will be associated.
	if err != nil {
		return fmt.Errorf("failed to add instance %v to target pool %v: %v", instanceName, pool, err)
	}
	return nil
}

func (r *Reconciler) deleteInstanceFromTargetPool(instanceLink string, pool string, region, instanceName string) error {
	_, err := r.computeService.TargetPoolsRemoveInstance(r.projectID, region, pool, instanceLink)
	if err != nil {
		return fmt.Errorf("failed to remove instance %v from target pool %v: %v", instanceName, pool, err)
	}
	return nil
}

func (r *Reconciler) getNetworkAddresses(ctx context.Context, instance *compute.Instance, machine *machinev1.Machine, zone string) ([]corev1.NodeAddress, error) {
	if len(instance.NetworkInterfaces) < 1 {
		return nil, fmt.Errorf("could not find network interfaces for instance %q", instance.Name)
	}
	networkInterface := instance.NetworkInterfaces[0]

	nodeAddresses := []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: networkInterface.NetworkIP}}
	for _, config := range networkInterface.AccessConfigs {
		nodeAddresses = append(nodeAddresses, corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: config.NatIP})
	}

	// Since we don't know when the project was created, we must account for
	// both types of internal-dns:
	// https://cloud.google.com/compute/docs/internal-dns#instance-fully-qualified-domain-names
	// [INSTANCE_NAME].[ZONE].c.[PROJECT_ID].internal (newer)
	nodeAddresses = append(nodeAddresses, corev1.NodeAddress{
		Type:    corev1.NodeInternalDNS,
		Address: fmt.Sprintf("%s.%s.c.%s.internal", machine.Name, zone, r.projectID),
	})
	// [INSTANCE_NAME].c.[PROJECT_ID].internal
	nodeAddresses = append(nodeAddresses, corev1.NodeAddress{
		Type:    corev1.NodeInternalDNS,
		Address: fmt.Sprintf("%s.c.%s.internal", machine.Name, r.projectID),
	})

	return nodeAddresses, nil
}
