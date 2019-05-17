package machine

import (
	"fmt"

	"github.com/pkg/errors"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
)

func instanceFromMachineContext(machineCtx *machineContext, machineName string) *compute.Instance {
	instance := compute.Instance{
		CanIpForward:       machineCtx.providerSpec.CanIPForward,
		DeletionProtection: machineCtx.providerSpec.DeletionProtection,
		Labels:             machineCtx.providerSpec.Labels,
		MachineType:        fmt.Sprintf("zones/%s/machineTypes/%s", machineCtx.providerSpec.Zone, machineCtx.providerSpec.MachineType),
		Name:               machineName,
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

	return &instance
}

func instanceGet(machineCtx *machineContext, name string) (*compute.Instance, error) {
	// Need to verify that our project/zone exists before checking
	// for machine name, as invalid project/zone produces same 404
	// error as no machine.
	if _, err := machineCtx.computeService.ZonesGet(machineCtx.projectID, machineCtx.providerSpec.Zone); err != nil {
		return nil, errors.Wrapf(err, "invalid zone %v", machineCtx.providerSpec.Zone)
	}

	instance, err := machineCtx.computeService.InstancesGet(machineCtx.projectID, machineCtx.providerSpec.Zone, name)
	if err != nil {
		e, ok := err.(*googleapi.Error)
		if ok && e.Code == 404 {
			return nil, nil
		}
		return nil, errors.Wrapf(err, "InstanceGet failed for machine %q", name)
	}

	return instance, nil
}
