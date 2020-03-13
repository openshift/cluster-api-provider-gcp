package computeservice

import (
	"net/http"

	"github.com/openshift/cluster-api-provider-gcp/pkg/version"
	"google.golang.org/api/compute/v1"
)

// GCPComputeService is a pass through wrapper for google.golang.org/api/compute/v1/compute
// to enable tests to mock this struct and control behavior.
type GCPComputeService interface {
	InstancesDelete(requestId string, project string, zone string, instance string) (*compute.Operation, error)
	InstancesInsert(project string, zone string, instance *compute.Instance) (*compute.Operation, error)
	InstancesGet(project string, zone string, instance string) (*compute.Instance, error)
	ZonesGet(project string, zone string) (*compute.Zone, error)
	ZoneOperationsGet(project string, zone string, operation string) (*compute.Operation, error)
	BasePath() string
	TargetPoolsGet(project string, region string, name string) (*compute.TargetPool, error)
	TargetPoolsAddInstance(project string, region string, name string, instance string) (*compute.Operation, error)
	TargetPoolsRemoveInstance(project string, region string, name string, instance string) (*compute.Operation, error)
	MachineTypesGet(project string, machineType string, zone string) (*compute.MachineType, error)
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
	service.UserAgent = "gcpprovider.openshift.io/" + version.Version.String()
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

func (c *computeService) InstancesGet(project string, zone string, instance string) (*compute.Instance, error) {
	return c.service.Instances.Get(project, zone, instance).Do()
}

func (c *computeService) InstancesDelete(requestId string, project string, zone string, instance string) (*compute.Operation, error) {
	return c.service.Instances.Delete(project, zone, instance).RequestId(requestId).Do()
}

func (c *computeService) ZonesGet(project string, zone string) (*compute.Zone, error) {
	return c.service.Zones.Get(project, zone).Do()
}

func (c *computeService) BasePath() string {
	return c.service.BasePath
}

func (c *computeService) TargetPoolsGet(project string, region string, name string) (*compute.TargetPool, error) {
	return c.service.TargetPools.Get(project, region, name).Do()
}

func (c *computeService) TargetPoolsAddInstance(project string, region string, name string, instanceLink string) (*compute.Operation, error) {
	rb := &compute.TargetPoolsAddInstanceRequest{
		Instances: []*compute.InstanceReference{
			{
				Instance: instanceLink,
			},
		},
	}
	return c.service.TargetPools.AddInstance(project, region, name, rb).Do()
}

func (c *computeService) TargetPoolsRemoveInstance(project string, region string, name string, instanceLink string) (*compute.Operation, error) {
	rb := &compute.TargetPoolsRemoveInstanceRequest{
		Instances: []*compute.InstanceReference{
			{
				Instance: instanceLink,
			},
		},
	}
	return c.service.TargetPools.RemoveInstance(project, region, name, rb).Do()
}

func (c *computeService) MachineTypesGet(project string, zone string, machineType string) (*compute.MachineType, error) {
	return c.service.MachineTypes.Get(project, zone, machineType).Do()
}
