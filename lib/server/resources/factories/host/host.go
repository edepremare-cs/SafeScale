/*
 * Copyright 2018-2020, CS Systemes d'Information, http://www.c-s.fr
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

package factory

import (
	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/operations/hosts"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/scerr"
)

// New creates an instance of resources.Host
func New(svc iaas.Service) (_ resources.Host, err error) {
	if svc == nil {
		return nil, scerr.InvalidInstanceError()
	}
	objh, err := hosts.New(svc)
	// return objh.(resources.Host), err
	return objh, err
}

// Load loads the metadata of host and returns an instance of resources.Host
func Load(task concurrency.Task, svc iaas.Service, ref string) (_ resources.Host, err error) {
	if task == nil {
		return nil, scerr.InvalidParameterError("task", "cannot be nil")
	}
	if svc == nil {
		return nil, scerr.InvalidParameterError("task", "cannot be nil")
	}
	if ref == "" {
		return nil, scerr.InvalidParameterError("ref", "cannot be empty string")
	}

	//FIXME: tracer...

	return hosts.Load(svc)
}
