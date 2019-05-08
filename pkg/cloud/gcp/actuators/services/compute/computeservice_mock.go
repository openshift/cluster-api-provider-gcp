package computeservice

import (
	compute "google.golang.org/api/compute/v1"
)

type GCPComputeServiceMock struct {
	mockInstancesInsert   func(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
	mockZoneOperationsGet func(project string, zone string, operation string) (*compute.Operation, error)
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
