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

package outscale

import (
	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

// GetRawConfigurationOptions ...
func (s stack) GetRawConfigurationOptions() (stacks.ConfigurationOptions, fail.Error) {
	return stacks.ConfigurationOptions{
		DNSList:          s.Options.Compute.DNSList,
		MetadataBucket:   s.Options.Metadata.Bucket,
		OperatorUsername: s.Options.Compute.OperatorUsername,
	}, nil
}

// GetRawAuthenticationOptions ...
func (s stack) GetRawAuthenticationOptions() (stacks.AuthenticationOptions, fail.Error) {
	return stacks.AuthenticationOptions{
		AccessKeyID:      s.Options.Identity.AccessKey,
		SecretAccessKey:  s.Options.Identity.SecretKey,
		Region:           s.Options.Compute.Region,
		AvailabilityZone: s.Options.Compute.Subregion,
		IdentityEndpoint: s.Options.Compute.URL,
	}, nil
}
