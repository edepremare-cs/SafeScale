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

package provideruse

import (
	brokerclient "github.com/CS-SI/SafeScale/broker/client"

	"github.com/CS-SI/SafeScale/providers"
)

//GetProviderService returns the service provider corresponding to the current Tenant
func GetProviderService() (*providers.Service, error) {
	tenant, err := brokerclient.New().Tenant.Get(brokerclient.DefaultTimeout)
	if err != nil {
		return nil, err
	}
	svc, err := providers.GetService(tenant.Name)
	if err != nil {
		return nil, err
	}
	return svc, nil
}
