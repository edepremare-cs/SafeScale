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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
)

func TestDefaults_Clone(t *testing.T) {
	ct := newClusterDefaults()
	ct.Image = "something"
	ct.GatewaySizing = abstract.HostEffectiveSizing{
		RAMSize: 3,
		GPUType: "NVidia",
	}

	clonedCt, ok := ct.Clone().(*ClusterDefaults)
	if !ok {
		t.Fail()
	}
	require.EqualValues(t, ct, clonedCt)

	assert.Equal(t, ct, clonedCt)
	clonedCt.GatewaySizing.GPUNumber = 7
	clonedCt.GatewaySizing.GPUType = "Culture"

	areEqual := reflect.DeepEqual(ct, clonedCt)
	if areEqual {
		t.Error("It's a shallow clone !")
		t.Fail()
	}
	require.NotEqualValues(t, ct, clonedCt)
}
