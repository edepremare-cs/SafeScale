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
	"time"

	"github.com/CS-SI/SafeScale/lib/server/resources/enums/volumeproperty"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
)

// VolumeDescription contains additional information describing the volume, in V1
// !!!FROZEN!!!
// Note: if tagged as FROZEN, must not be changed ever.
//       Create a new version instead with needed supplemental fields
type VolumeDescription struct {
	// Purpose contains the reason of the existence of the volume
	Purpose string `json:"purpose,omitempty"`
	// Created contains the time of creation of the volume
	Created time.Time `json:"created,omitempty"`
}

// NewVolumeDescription ...
func NewVolumeDescription() *VolumeDescription {
	return &VolumeDescription{}
}

// IsNull ...
func (vd *VolumeDescription) IsNull() bool {
	return vd == nil || (vd.Created.IsZero() && vd.Purpose == "")
}

// Clone ...
func (vd VolumeDescription) Clone() data.Clonable {
	return NewVolumeDescription().Replace(&vd)
}

// Replace ...
func (vd *VolumeDescription) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if vd == nil || p == nil {
		return vd
	}

	*vd = *p.(*VolumeDescription)
	return vd
}

// VolumeAttachments contains host ids where the volume is attached
// !!!FROZEN!!!
// Note: if tagged as FROZEN, must not be changed ever.
//       Create a new version instead with needed supplemental fields
type VolumeAttachments struct {
	Shareable bool              `json:"shareable,omitempty"` // Tells if the volume can be shared with multiple host
	Hosts     map[string]string `json:"hosts,omitempty"`     // Contains the name of the hosts mounting the volume, indexed by host ID
}

// NewVolumeAttachments ...
func NewVolumeAttachments() *VolumeAttachments {
	return &VolumeAttachments{
		Hosts: map[string]string{},
	}
}

// IsNull ...
// (data.Clonable interface)
func (va *VolumeAttachments) IsNull() bool {
	return va == nil || len(va.Hosts) == 0
}

// Clone ... (data.Clonable interface)
func (va VolumeAttachments) Clone() data.Clonable {
	return NewVolumeAttachments().Replace(&va)
}

// Replace ... (data.Clonable interface)
func (va *VolumeAttachments) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if va == nil || p == nil {
		return va
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*VolumeAttachments) // nolint
	*va = *src
	va.Hosts = make(map[string]string, len(src.Hosts))
	for k, v := range src.Hosts {
		va.Hosts[k] = v
	}
	return va
}

func init() {
	serialize.PropertyTypeRegistry.Register("resources.volume", volumeproperty.DescriptionV1, NewVolumeDescription())
	serialize.PropertyTypeRegistry.Register("resources.volume", volumeproperty.AttachedV1, NewVolumeAttachments())
}
