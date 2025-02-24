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

package client

import (
	"time"

	"github.com/CS-SI/SafeScale/lib/protocol"
	"github.com/CS-SI/SafeScale/lib/server/utils"
)

// host is the safescale client part handling hosts
type image struct {
	// session is not used currently
	session *Session
}

// List return the list of available images on the current tenant
func (img image) List(all bool, timeout time.Duration) (*protocol.ImageList, error) {
	img.session.Connect()
	defer img.session.Disconnect()
	service := protocol.NewImageServiceClient(img.session.connection)
	ctx, xerr := utils.GetContext(true)
	if xerr != nil {
		return nil, xerr
	}

	return service.List(ctx, &protocol.ImageListRequest{All: all})
}
