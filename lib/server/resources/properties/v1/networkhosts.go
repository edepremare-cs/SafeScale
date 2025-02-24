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
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/networkproperty"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
)

// NetworkHosts contains information about hosts connected to the network
type NetworkHosts struct {
	ByID   map[string]string `json:"by_id,omitempty"`   // list of host names, indexed by host id
	ByName map[string]string `json:"by_name,omitempty"` // list of host IDs, indexed by host name
}

// NewNetworkHosts ...
func NewNetworkHosts() *NetworkHosts {
	return &NetworkHosts{
		ByID:   map[string]string{},
		ByName: map[string]string{},
	}
}

// Reset resets the content of the property
func (nh *NetworkHosts) Reset() {
	*nh = NetworkHosts{
		ByID:   map[string]string{},
		ByName: map[string]string{},
	}
}

// IsNull ... (data.Clonable interface)
func (nh *NetworkHosts) IsNull() bool {
	return nh == nil || len(nh.ByID) == 0
}

// Clone ... (data.Clonable interface)
func (nh NetworkHosts) Clone() data.Clonable {
	return NewNetworkHosts().Replace(&nh)
}

// Replace ... (data.Clonable interface)
func (nh *NetworkHosts) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if nh == nil || p == nil {
		return nh
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*NetworkHosts) // nolint
	nh.ByID = make(map[string]string, len(src.ByID))
	for k, v := range src.ByID {
		nh.ByID[k] = v
	}
	nh.ByName = make(map[string]string, len(src.ByName))
	for k, v := range src.ByName {
		nh.ByName[k] = v
	}
	return nh
}

func init() {
	serialize.PropertyTypeRegistry.Register("resources.network", networkproperty.HostsV1, NewNetworkHosts())
}
