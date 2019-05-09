package computeservice

import (
	compute "google.golang.org/api/compute/v1"
)

type GCPComputeServiceMock struct {
	mockInstancesInsert   func(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
	mockZoneOperationsGet func(project string, zone string, operation string) (*compute.Operation, error)
	mockInstancesGet      func(project string, zone string, instance string) (*compute.Instance, error)
}

func (c *GCPComputeServiceMock) InstancesInsert(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
	if c.mockInstancesInsert == nil {
		return nil, nil
	}
	return c.mockInstancesInsert(project, zone, instance)
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

func NewComputeServiceMock() (*compute.Instance, *GCPComputeServiceMock) {
	var receivedInstance compute.Instance
	computeServiceMock := GCPComputeServiceMock{
		mockInstancesInsert: func(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
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
