/*
 * Copyright 2018, CS Systemes d'Information, http://www.c-s.fr
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

package cloudferro_test

import (
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"os"
	"testing"

	"github.com/CS-SI/SafeScale/providers"
	"github.com/CS-SI/SafeScale/providers/tests"
)

var tester *tests.ClientTester

func getClient() (*tests.ClientTester, error) {
	tenant_name := "cloudferro"
	if tenant_override := os.Getenv("TEST_CLOUDFERRO"); tenant_override != "" {
		tenant_name = tenant_override
	}
	if tester == nil {
		service, err := providers.GetService(tenant_name)
		if err != nil {
			fmt.Println(err.Error())
			return nil, errors.New(fmt.Sprintf("You must provide a VALID tenant [%v], check your environment variables and your Safescale configuration files", tenant_name))
		}
		tester = &tests.ClientTester{
			Service: *service,
		}
	}
	return tester, nil
}

func Test_ListImages(t *testing.T) {
	tt, err := getClient()
	require.Nil(t, err)
	tt.ListImages(t)
}
