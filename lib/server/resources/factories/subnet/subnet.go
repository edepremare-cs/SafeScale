/*
 * Copyright 2018-2021, CS Systemes d'Information, http://csgroup.eu
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package subnet contains methods to load or create instance of resources.Subnet
package subnet

import (
	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/operations"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

// List returns a list of available subnets
func List(/* ctx context.Context, */svc iaas.Service, networkID string, all bool) ([]*abstract.Subnet, fail.Error) {
	if task == nil {
		return nil, fail.InvalidParameterCannotBeNilError("task")
	}
	if task.Aborted() {
		return nil, fail.AbortedError(nil, "aborted")
	}
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}

	return operations.ListSubnets(task, svc, networkID, all)
}

// New creates an instance of resources.Subnet
func New(svc iaas.Service) (resources.Subnet, fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}

	return operations.NewSubnet(svc)
}

// Load loads the metadata of a subnet and returns an instance of resources.Subnet
func Load(/* ctx context.Context, */svc iaas.Service, networkRef, subnetRef string) (resources.Subnet, fail.Error) {
	if task == nil {
		return nil, fail.InvalidParameterCannotBeNilError("task")
	}
	if task.Aborted() {
		return nil, fail.AbortedError(nil, "aborted")
	}
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}
	if subnetRef == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("subnetRef")
	}

	return operations.LoadSubnet(task, svc, networkRef, subnetRef)
}
