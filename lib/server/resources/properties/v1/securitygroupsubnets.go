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
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/securitygroupproperty"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
)

// SecurityGroupSubnets contains information about attached subnets to a security group
// not FROZEN yet
// Note: if tagged as FROZEN, must not be changed ever.
//       Create a new version instead with needed supplemental fields
type SecurityGroupSubnets struct {
	DefaultFor string                        `json:"default_for,omitempty"` // contains the ID of the subnet for which the Security Group is default
	ByID       map[string]*SecurityGroupBond `json:"by_id,omitempty"`       // contains the bonds of a Security Group with Subnets using it, indexed on Subnets' ID
	ByName     map[string]string             `json:"by_name,omitempty"`     // contains the Subnets' ID indexed on Subnets' Name
}

// NewSecurityGroupSubnets ...
func NewSecurityGroupSubnets() *SecurityGroupSubnets {
	return &SecurityGroupSubnets{
		ByID:   map[string]*SecurityGroupBond{},
		ByName: map[string]string{},
	}
}

// IsNull ...
func (sgn *SecurityGroupSubnets) IsNull() bool {
	return sgn == nil || len(sgn.ByID) == 0
}

// Clone ...
func (sgn SecurityGroupSubnets) Clone() data.Clonable {
	return NewSecurityGroupSubnets().Replace(&sgn)
}

// Replace ...
func (sgn *SecurityGroupSubnets) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if sgn == nil || p == nil {
		return sgn
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*SecurityGroupSubnets) // nolint
	sgn.ByID = make(map[string]*SecurityGroupBond, len(src.ByID))
	for k, v := range src.ByID {
		// FIXME: Replace should also return an error or generics
		sgn.ByID[k], _ = v.Clone().(*SecurityGroupBond) // nolint
	}
	sgn.ByName = make(map[string]string, len(src.ByName))
	for k, v := range src.ByName {
		sgn.ByName[k] = v
	}
	return sgn
}

func init() {
	serialize.PropertyTypeRegistry.Register("resources.security-group", securitygroupproperty.SubnetsV1, NewSecurityGroupSubnets())
}
