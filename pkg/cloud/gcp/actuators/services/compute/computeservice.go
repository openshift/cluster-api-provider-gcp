package computeservice

import (
	"net/http"

	"google.golang.org/api/compute/v1"
)

// GCPComputeService is a pass through wrapper for google.golang.org/api/compute/v1/compute
// to enable tests to mock this struct and control behavior.
type GCPComputeService interface {
	InstancesInsert(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
	ZoneOperationsGet(project string, zone string, operation string) (*compute.Operation, error)
	InstancesGet(project string, zone string, instance string) (*compute.Instance, error)
}

type computeService struct {
	service *compute.Service
}

// NewComputeService return a new computeService
func NewComputeService(oauthClient *http.Client) (*computeService, error) {
	service, err := compute.New(oauthClient)
	if err != nil {
		return nil, err
	}
	return &computeService{
		service: service,
	}, nil
}

// InstancesInsert is a pass through wrapper for compute.Service.Instances.Insert(...)
func (c *computeService) InstancesInsert(project string, zone string, instance *compute.Instance) (*compute.Operation, error) {
	return c.service.Instances.Insert(project, zone, instance).Do()
}

// ZoneOperationsGet is a pass through wrapper for compute.Service.ZoneOperations.Get(...)
func (c *computeService) ZoneOperationsGet(project string, zone string, operation string) (*compute.Operation, error) {
	return c.service.ZoneOperations.Get(project, zone, operation).Do()
}

// A pass through wrapper for compute.Service.Instances.Get(...)
func (c *computeService) InstancesGet(project string, zone string, instance string) (*compute.Instance, error) {
	return c.service.Instances.Get(project, zone, instance).Do()
}
