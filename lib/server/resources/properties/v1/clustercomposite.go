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
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/clusterproperty"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
)

// ClusterComposite ...
type ClusterComposite struct {
	// Array of tenants hosting a multi-tenant cluster (multi starting from 1)
	Tenants []string `json:"tenants,omitempty"`
}

func newClusterComposite() *ClusterComposite {
	return &ClusterComposite{
		Tenants: []string{},
	}
}

// IsNull ...
// satisfies interface data.Clonable
func (c *ClusterComposite) IsNull() bool {
	return c == nil || len(c.Tenants) == 0
}

// Clone ...
// satisfies interface data.Clonable
func (c ClusterComposite) Clone() data.Clonable {
	return newClusterComposite().Replace(&c)
}

// Replace ...
// satisfies interface data.Clonable
func (c *ClusterComposite) Replace(p data.Clonable) data.Clonable {
	// Do not test with isNull(), it's allowed to clone a null value...
	if c == nil || p == nil {
		return c
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*ClusterComposite) // nolint
	c.Tenants = make([]string, len(src.Tenants))
	copy(c.Tenants, src.Tenants)
	return c
}

func init() {
	serialize.PropertyTypeRegistry.Register("resources.cluster", clusterproperty.CompositeV1, newClusterComposite())
}
