/*
Copyright The Kubernetes Authors.
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package machineset

import (
	"fmt"
	"sync"

	computeservice "github.com/openshift/cluster-api-provider-gcp/pkg/cloud/gcp/actuators/services/compute"
	gce "google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"k8s.io/klog/v2"
)

// machineTypeKey is used to identify MachineType.
type machineTypeKey struct {
	zone        string
	machineType string
}

// machineTypesCache is used for caching machine types.
type machineTypesCache struct {
	cacheMutex        sync.Mutex
	machineTypesCache map[machineTypeKey]*gce.MachineType
}

// newMachineTypesCache creates empty machineCache.
func newMachineTypesCache() *machineTypesCache {
	return &machineTypesCache{
		machineTypesCache: map[machineTypeKey]*gce.MachineType{},
	}
}

// getMachineTypeFromCache retrieves machine type from cache under lock.
func (mc *machineTypesCache) getMachineTypeFromCache(gcpService computeservice.GCPComputeService, projectID string, zone string, machineType string) (*gce.MachineType, error) {
	mc.cacheMutex.Lock()
	defer mc.cacheMutex.Unlock()

	// Machine Type already fetched from GCE
	if mt, ok := mc.machineTypesCache[machineTypeKey{zone, machineType}]; ok {
		return mt, nil
	}

	mt, err := gcpService.MachineTypesGet(projectID, zone, machineType)
	if err != nil {
		if !isNotFoundError(err) {
			return nil, fmt.Errorf("error fetching machine type %q in zone %q: %v", machineType, zone, err)
		}
		klog.Error("Unable to set scale from zero annotations: unknown instance type: %s", machineType)
		klog.Error("Autoscaling from zero will not work. To fix this, manually populate machine annotations for your instance type: %v", []string{cpuKey, memoryKey})
		// Returning no instance type and no error to prevent further reconciliation
		return nil, nil
	}

	mc.machineTypesCache[machineTypeKey{zone, machineType}] = mt
	return mt, nil
}

func isNotFoundError(err error) bool {
	switch t := err.(type) {
	case *googleapi.Error:
		return t.Code == 404
	}
	return false
}
