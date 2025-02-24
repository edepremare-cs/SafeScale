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

package resources

import (
	"context"

	"github.com/CS-SI/SafeScale/lib/protocol"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/data/observer"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

// Bucket GetBucket defines the interface to manipulate Object Storage buckets
type Bucket interface {
	Metadata
	data.Identifiable
	cache.Cacheable
	observer.Observable

	Browse(ctx context.Context, callback func(bucket *abstract.ObjectStorageBucket) fail.Error) fail.Error
	Create(ctx context.Context, name string) fail.Error
	Delete(ctx context.Context) fail.Error
	Mount(ctx context.Context, hostname string, path string) fail.Error
	ToProtocol() (*protocol.BucketResponse, fail.Error)
	Unmount(ctx context.Context, hostname string) fail.Error
}
