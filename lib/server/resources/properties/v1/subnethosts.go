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

package propertiesv1

import (
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/subnetproperty"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
)

// SubnetHosts contains information about hosts attached to the subnet
type SubnetHosts struct {
	ByID   map[string]string `json:"by_id,omitempty"`   // list of host names, indexed by host id
	ByName map[string]string `json:"by_name,omitempty"` // list of host IDs, indexed by host name
}

// NewSubnetHosts ...
func NewSubnetHosts() *SubnetHosts {
	return &SubnetHosts{
		ByID:   map[string]string{},
		ByName: map[string]string{},
	}
}

// IsNull
// ... (data.Clonable interface)
func (sh *SubnetHosts) IsNull() bool {
	return sh == nil || len(sh.ByID) == 0
}

// Clone ... (data.Clonable interface)
func (sh SubnetHosts) Clone() data.Clonable {
	return NewSubnetHosts().Replace(&sh)
}

// Replace ... (data.Clonable interface)
func (sh *SubnetHosts) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if sh == nil || p == nil {
		return sh
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*SubnetHosts) // nolint
	sh.ByID = make(map[string]string, len(src.ByID))
	for k, v := range src.ByID {
		sh.ByID[k] = v
	}
	sh.ByName = make(map[string]string, len(src.ByName))
	for k, v := range src.ByName {
		sh.ByName[k] = v
	}
	return sh
}

func init() {
	serialize.PropertyTypeRegistry.Register("resources.subnet", subnetproperty.HostsV1, NewSubnetHosts())
}
