package computeservice

import (
	compute "google.golang.org/api/compute/v1"
)

const (
	NoMachinesInPool  = "NoMachinesInPool"
	WithMachineInPool = "WithMachineInPool"
)

type GCPComputeServiceMock struct {
	MockInstancesInsert   func(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
	mockZoneOperationsGet func(project string, zone string, operation string) (*compute.Operation, error)
	mockInstancesGet      func(project string, zone string, instance string) (*compute.Instance, error)
}

func (c *GCPComputeServiceMock) InstancesInsert(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
	if c.MockInstancesInsert == nil {
		return nil, nil
	}
	return c.MockInstancesInsert(project, zone, instance)
}

func (c *GCPComputeServiceMock) InstancesDelete(requestId string, project string, zone string, instance string) (*compute.Operation, error) {
	return &compute.Operation{
		Status: "DONE",
	}, nil
}

func (c *GCPComputeServiceMock) ZoneOperationsGet(project string, zone string, operation string) (*compute.Operation, error) {
	if c.mockZoneOperationsGet == nil {
		return nil, nil
	}
	return c.mockZoneOperationsGet(project, zone, operation)
}

func (c *GCPComputeServiceMock) InstancesGet(project string, zone string, instance string) (*compute.Instance, error) {
	return &compute.Instance{
		Name:         instance,
		Zone:         zone,
		MachineType:  "n1-standard-1",
		CanIpForward: true,
		NetworkInterfaces: []*compute.NetworkInterface{
			{
				NetworkIP: "10.0.0.15",
				AccessConfigs: []*compute.AccessConfig{
					{
						NatIP: "35.243.147.143",
					},
				},
			},
		},
		Status: "RUNNING",
	}, nil
}

func (c *GCPComputeServiceMock) ZonesGet(project string, zone string) (*compute.Zone, error) {
	return nil, nil
}

func (c *GCPComputeServiceMock) BasePath() string {
	return "path/"
}

func (c *GCPComputeServiceMock) TargetPoolsGet(project string, region string, name string) (*compute.TargetPool, error) {
	if region == NoMachinesInPool {
		return &compute.TargetPool{}, nil
	}
	if region == WithMachineInPool {
		return &compute.TargetPool{
			Instances: []string{
				"https://www.googleapis.com/compute/v1/projects/testProject/zones/zone1/instances/testInstance",
			},
		}, nil
	}
	return nil, nil
}

func (c *GCPComputeServiceMock) TargetPoolsAddInstance(project string, region string, name string, instance string) (*compute.Operation, error) {
	return nil, nil
}

func (c *GCPComputeServiceMock) TargetPoolsRemoveInstance(project string, region string, name string, instance string) (*compute.Operation, error) {
	return nil, nil
}

func (c *GCPComputeServiceMock) MachineTypesGet(project string, zone string, machineType string) (*compute.MachineType, error) {
	return nil, nil
}

func NewComputeServiceMock() (*compute.Instance, *GCPComputeServiceMock) {
	var receivedInstance compute.Instance
	computeServiceMock := GCPComputeServiceMock{
		MockInstancesInsert: func(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
			receivedInstance = *instance
			return &compute.Operation{
				Status: "DONE",
			}, nil
		},
		mockZoneOperationsGet: func(project string, zone string, operation string) (*compute.Operation, error) {
			return &compute.Operation{
				Status: "DONE",
			}, nil
		},
	}
	return &receivedInstance, &computeServiceMock
}
