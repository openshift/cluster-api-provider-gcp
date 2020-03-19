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
		return nil, fmt.Errorf("error fetching machine type %q in zone %q: %v", machineType, zone, err)
	}

	mc.machineTypesCache[machineTypeKey{zone, machineType}] = mt
	return mt, nil
}
