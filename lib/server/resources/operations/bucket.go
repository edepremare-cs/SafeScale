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

package operations

//go:generate rice embed-go

import (
	"context"
	"reflect"
	"strings"
	"sync"

	"github.com/CS-SI/SafeScale/lib/protocol"
	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/bucketproperty"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hostproperty"
	propertiesv1 "github.com/CS-SI/SafeScale/lib/server/resources/properties/v1"
	"github.com/CS-SI/SafeScale/lib/system/bucketfs"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

const (
	bucketKind = "bucket"
	// bucketsFolderName is the name of the object storage MetadataFolder used to store buckets info
	bucketsFolderName = "buckets"
)

// bucket describes a bucket and satisfies interface resources.ObjectStorageBucket
type bucket struct {
	*MetadataCore

	lock sync.RWMutex
}

// NewBucket intanciates bucket struct
func NewBucket(svc iaas.Service) (resources.Bucket, fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}

	coreInstance, xerr := NewCore(svc, bucketKind, bucketsFolderName, &abstract.ObjectStorageBucket{})
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	instance := &bucket{
		MetadataCore: coreInstance,
	}
	return instance, nil
}

// LoadBucket instantiates a bucket struct and fill it with Provider metadata of Object Storage ObjectStorageBucket
func LoadBucket(svc iaas.Service, name string) (b resources.Bucket, xerr fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}
	if name == "" {
		return nil, fail.InvalidParameterError("name", "cannot be empty string")
	}

	bucketCache, xerr := svc.GetCache(bucketKind)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	cacheOptions := iaas.CacheMissOption(
		func() (cache.Cacheable, fail.Error) { return onBucketCacheMiss(svc, name) },
		temporal.GetMetadataTimeout(),
	)
	cacheEntry, xerr := bucketCache.Get(name, cacheOptions...)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			debug.IgnoreError(xerr)
			// rewrite NotFoundError, user does not bother about metadata stuff
			return nil, fail.NotFoundError("failed to find Bucket '%s'", name)
		default:
			return nil, xerr
		}
	}

	var ok bool
	b, ok = cacheEntry.Content().(resources.Bucket)
	if !ok {
		return nil, fail.InconsistentError("cache content should be a resources.Bucket", name)
	}

	if b == nil {
		return nil, fail.InconsistentError("nil value found in Bucket cache for key '%s'", name)
	}

	_ = cacheEntry.LockContent()

	return b, nil
}

func onBucketCacheMiss(svc iaas.Service, ref string) (cache.Cacheable, fail.Error) {
	bucketInstance, innerXErr := NewBucket(svc)
	if innerXErr != nil {
		return nil, innerXErr
	}

	// TODO: core.ReadByID() does not check communication failure, side effect of limitations of Stow (waiting for stow replacement by rclone)
	if innerXErr = bucketInstance.Read(ref); innerXErr != nil {
		return nil, innerXErr
	}

	return bucketInstance, nil
}

// IsNull tells if the instance corresponds to null value
func (instance *bucket) IsNull() bool {
	return instance == nil || instance.MetadataCore == nil || instance.MetadataCore.IsNull()
}

// carry ...
func (instance *bucket) carry(clonable data.Clonable) (ferr fail.Error) {
	if instance == nil {
		return fail.InvalidInstanceError()
	}
	if !instance.IsNull() {
		return fail.InvalidInstanceContentError("instance", "is not null value, cannot overwrite")
	}
	if clonable == nil {
		return fail.InvalidParameterCannotBeNilError("clonable")
	}
	identifiable, ok := clonable.(data.Identifiable)
	if !ok {
		return fail.InvalidParameterError("clonable", "must also satisfy interface 'data.Identifiable'")
	}

	kindCache, xerr := instance.GetService().GetCache(instance.MetadataCore.GetKind())
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	xerr = kindCache.ReserveEntry(identifiable.GetID(), temporal.GetMetadataTimeout())
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}
	defer func() {
		ferr = debug.InjectPlannedFail(ferr)
		if ferr != nil {
			if derr := kindCache.FreeEntry(identifiable.GetID()); derr != nil {
				_ = ferr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to free %s cache entry for key '%s'", instance.MetadataCore.GetKind(), identifiable.GetID()))
			}
		}
	}()

	// Note: do not validate parameters, this call will do it
	xerr = instance.MetadataCore.Carry(clonable)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	cacheEntry, xerr := kindCache.CommitEntry(identifiable.GetID(), instance)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	cacheEntry.LockContent()

	return nil
}

// Browse walks through Bucket metadata folder and executes a callback for each entry
func (instance *bucket) Browse(
	ctx context.Context, callback func(storageBucket *abstract.ObjectStorageBucket) fail.Error,
) (outerr fail.Error) {
	defer fail.OnPanic(&outerr)

	// Note: Do not test with Isnull here, as Browse may be used from null value
	if instance == nil {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if callback == nil {
		return fail.InvalidParameterCannotBeNilError("callback")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return xerr
			}
		default:
			return xerr
		}
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.bucket")).WithStopwatch().Entering()
	defer tracer.Exiting()

	instance.lock.RLock()
	defer instance.lock.RUnlock()

	return instance.MetadataCore.BrowseFolder(
		func(buf []byte) (innerXErr fail.Error) {
			if task.Aborted() {
				return fail.AbortedError(nil, "aborted")
			}

			ab := abstract.NewObjectStorageBucket()
			if innerXErr = ab.Deserialize(buf); innerXErr != nil {
				return innerXErr
			}

			return callback(ab)
		},
	)
}

// // GetHost ...
// func (instance *bucket) GetHost(ctx context.Context) (_ string, xerr fail.Error) {
// 	if instance == nil || instance.IsNull() {
// 		return "", fail.InvalidInstanceError()
// 	}
// 	if ctx == nil {
// 		return "", fail.InvalidParameterCannotBeNilError("ctx")
// 	}
//
// 	task, xerr := concurrency.TaskFromContext(ctx)
// 	xerr = debug.InjectPlannedFail(xerr)
// 	if xerr != nil {
// 		switch xerr.(type) {
// 		case *fail.ErrNotAvailable:
// 			task, xerr = concurrency.VoidTask()
// 			if xerr != nil {
// 				return "", xerr
// 			}
// 		default:
// 			return "", xerr
// 		}
// 	}
//
// 	if task.Aborted() {
// 		return "", fail.AbortedError(nil, "aborted")
// 	}
//
// 	instance.lock.RLock()
// 	defer instance.lock.RUnlock()
//
// 	var res string
// 	xerr = instance.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
// 		ab, ok := clonable.(*abstract.ObjectStorageBucket)
// 		if !ok {
// 			return fail.InconsistentError("'*abstract.ObjectStorageBucket' expected, '%s' provided", reflect.TypeOf(clonable).String())
// 		}
//
// 		res = ab.Host
// 		return nil
// 	})
// 	xerr = debug.InjectPlannedFail(xerr)
// 	if xerr != nil {
// 		return res, xerr
// 	}
//
// 	return res, nil
// }
//
// // GetMountPoint ...
// func (instance *bucket) GetMountPoint(ctx context.Context) (string, fail.Error) {
// 	if instance == nil || instance.IsNull() {
// 		return "", fail.InvalidInstanceError()
// 	}
// 	if ctx == nil {
// 		return "", fail.InvalidParameterCannotBeNilError("ctx")
// 	}
//
// 	task, xerr := concurrency.TaskFromContext(ctx)
// 	xerr = debug.InjectPlannedFail(xerr)
// 	if xerr != nil {
// 		switch xerr.(type) {
// 		case *fail.ErrNotAvailable:
// 			task, xerr = concurrency.VoidTask()
// 			if xerr != nil {
// 				return "", xerr
// 			}
// 		default:
// 			return "", xerr
// 		}
// 	}
//
// 	if task.Aborted() {
// 		return "", fail.AbortedError(nil, "aborted")
// 	}
//
// 	instance.lock.RLock()
// 	defer instance.lock.RUnlock()
//
// 	var res string
// 	xerr = instance.Inspect(func(clonable data.Clonable, _ *serialize.JSONProperties) fail.Error {
// 		ab, ok := clonable.(*abstract.ObjectStorageBucket)
// 		if !ok {
// 			return fail.InconsistentError("'*abstract.ObjectStorageBucket' expected, '%s' provided", reflect.TypeOf(clonable).String())
// 		}
// 		res = ab.MountPoint
// 		return nil
// 	})
// 	xerr = debug.InjectPlannedFail(xerr)
// 	if xerr != nil {
// 		logrus.Errorf(xerr.Error())
// 	}
// 	return res, nil
// }

// Create a bucket
func (instance *bucket) Create(ctx context.Context, name string) (xerr fail.Error) {
	defer fail.OnPanic(&xerr)

	// note: do not test IsNull() here, it's expected to be IsNull() actually
	if instance == nil {
		return fail.InvalidInstanceError()
	}
	if !instance.IsNull() {
		bucketName := instance.GetName()
		if bucketName != "" {
			return fail.NotAvailableError("already carrying Share '%s'", bucketName)
		}
		return fail.InvalidInstanceContentError("s", "is not null value")
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if name == "" {
		return fail.InvalidParameterError("name", "cannot be empty string")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return xerr
			}
		default:
			return xerr
		}
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, true, "('"+name+"')").WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	instance.lock.Lock()
	defer instance.lock.Unlock()

	svc := instance.GetService()

	// -- check if bucket already exist in SafeScale
	bucketInstance, xerr := LoadBucket(svc, name)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// no bucket with this name managed by SafeScale, continue
			debug.IgnoreError(xerr)
		default:
			return xerr
		}
	}
	if bucketInstance != nil {
		bucketInstance.Released()
		return abstract.ResourceDuplicateError("bucket", name)
	}

	// -- check if bucket already exist on provider side
	ab, xerr := svc.InspectBucket(name)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			debug.IgnoreError(xerr)
		default:
			if strings.Contains(xerr.Error(), "not found") {
				debug.IgnoreError(xerr)
				break
			}
			return xerr
		}
	}
	if !ab.IsNull() {
		return abstract.ResourceDuplicateError("bucket", name)
	}

	// -- create bucket
	ab, xerr = svc.CreateBucket(name)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	// -- write metadata
	return instance.carry(&ab)
}

// Delete a bucket
func (instance *bucket) Delete(ctx context.Context) (xerr fail.Error) {
	if instance == nil || instance.IsNull() {
		return fail.InvalidInstanceError()
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return xerr
			}
		default:
			return xerr
		}
	}

	tracer := debug.NewTracer(task, true, "").WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	instance.lock.Lock()
	defer instance.lock.Unlock()

	// -- check Bucket is not still mounted
	xerr = instance.Review(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(bucketproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.BucketMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.BucketMount' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			if len(mountsV1.ByHostID) > 0 {
				return fail.NotAvailableError("still mounted on some Hosts")
			}

			return nil
		})
	})
	if xerr != nil {
		return xerr
	}

	// -- delete Bucket
	xerr = instance.GetService().DeleteBucket(instance.GetName())
	if xerr != nil {
		if strings.Contains(xerr.Error(), "not found") {
			return fail.NotFoundError("failed to find Bucket '%s'", instance.GetName())
		}
		return xerr
	}

	// -- delete metadata
	return instance.MetadataCore.Delete()
}

// Mount a bucket on a host on the given mount point
// Returns:
// - nil: mount successful
// - *fail.ErrNotFound: Host not found
// - *fail.ErrDuplicate: already mounted on Host
// - *fail.ErrNotAvailable: already mounted
func (instance *bucket) Mount(ctx context.Context, hostName, path string) (outerr fail.Error) {
	if instance == nil || instance.IsNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if hostName == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("hostName")
	}
	if path == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("path")
	}

	defer fail.OnPanic(&outerr)

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return xerr
			}
		default:
			return xerr
		}
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, true, "('%s', '%s')", hostName, path).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&outerr, tracer.TraceMessage(""))

	instance.lock.Lock()
	defer instance.lock.Unlock()

	hostInstance, xerr := LoadHost(instance.GetService(), hostName)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return fail.Wrap(xerr, "failed to mount bucket '%s' on Host '%s'", instance.GetName(), hostName)
	}
	defer hostInstance.Released()

	// -- check if Bucket is already mounted on any Host (only one Mount by Bucket allowed by design, to mitigate sync issues induced by Object Storage)
	xerr = instance.Review(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Inspect(bucketproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.BucketMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.BucketMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			// First check if mounted on Host...
			if mountPath, ok := mountsV1.ByHostName[hostInstance.GetName()]; ok {
				return fail.DuplicateError("there is already a mount of Bucket '%s' on Host '%s' in folder '%s'", instance.GetName(), hostInstance.GetName(), mountPath)
			}

			// Second check if already mounted on another Host...
			if len(mountsV1.ByHostName) > 0 {
				for hostName := range mountsV1.ByHostName {
					return fail.NotAvailableError("already mounted on Host '%s'", hostName)
				}
			}

			return nil
		})
	})
	if xerr != nil {
		return xerr
	}

	svc := instance.GetService()
	authOpts, xerr := svc.GetAuthenticationOptions()
	if xerr != nil {
		return xerr
	}

	bucketFSClient, xerr := bucketfs.NewClient(hostInstance)
	if xerr != nil {
		return xerr
	}

	// -- assemble parameters for mount description
	osConfig := svc.ObjectStorageConfiguration()

	mountPoint := path
	if path == abstract.DefaultBucketMountPoint {
		mountPoint = abstract.DefaultBucketMountPoint + instance.GetName()
	}

	desc := bucketfs.Description{
		BucketName: instance.GetName(),
		Protocol:   svc.Protocol(),
		MountPoint: mountPoint,
	}
	if anon, ok := authOpts.Config("AuthURL"); ok {
		if aurl, ok := anon.(string); ok {
			desc.AuthURL = aurl
		}
	}
	desc.Endpoint = osConfig.Endpoint

	// needed value for Description.ProjectName may come from various config entries depending on the Cloud Provider
	if anon, ok := authOpts.Config("ProjectName"); ok {
		desc.ProjectName, ok = anon.(string)
		if !ok {
			return fail.InconsistentError("anon should be a string")
		}
	} else if anon, ok := authOpts.Config("ProjectID"); ok {
		desc.ProjectName, ok = anon.(string)
		if !ok {
			return fail.InconsistentError("anon should be a string")
		}
	} else if anon, ok := authOpts.Config("TenantName"); ok {
		desc.ProjectName, ok = anon.(string)
		if !ok {
			return fail.InconsistentError("anon should be a string")
		}
	}

	desc.Username = osConfig.User
	desc.Password = osConfig.SecretKey
	desc.Region = osConfig.Region

	// -- execute the mount
	xerr = bucketFSClient.Mount(ctx, desc)
	if xerr != nil {
		return xerr
	}

	// -- update metadata of Bucket
	xerr = instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(bucketproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.BucketMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.BucketMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			mountsV1.ByHostID[hostInstance.GetID()] = mountPoint
			mountsV1.ByHostName[hostInstance.GetName()] = mountPoint
			return nil
		})
	})
	if xerr != nil {
		return xerr
	}

	// -- update metadata of Host
	return hostInstance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(hostproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.HostMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			mountsV1.BucketMounts[instance.GetName()] = mountPoint
			return nil
		})
	})
}

// Unmount a bucket
func (instance *bucket) Unmount(ctx context.Context, hostName string) (xerr fail.Error) {
	if instance == nil || instance.IsNull() {
		return fail.InvalidInstanceError()
	}
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}
	if hostName == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("hostName")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return xerr
			}
		default:
			return xerr
		}
	}

	if task.Aborted() {
		return fail.AbortedError(nil, "aborted")
	}

	tracer := debug.NewTracer(task, true, "('%s')", hostName).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	instance.lock.Lock()
	defer instance.lock.Unlock()

	svc := instance.GetService()

	hostInstance, xerr := LoadHost(svc, hostName)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return xerr
	}

	var mountPoint string
	bucketName := instance.GetName()
	mounts, xerr := hostInstance.GetMounts()
	for k, v := range mounts.BucketMounts {
		if k == bucketName {
			mountPoint = v
			break
		}
	}
	if mountPoint == "" {
		return fail.NotFoundError("failed to find corresponding mount on Host")
	}

	bucketFSClient, xerr := bucketfs.NewClient(hostInstance)
	if xerr != nil {
		return xerr
	}

	description := bucketfs.Description{
		BucketName: bucketName,
		MountPoint: mountPoint,
	}
	xerr = bucketFSClient.Unmount(ctx, description)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// If mount is not found on remote server, consider unmount as successful
			debug.IgnoreError(xerr)
		default:
			return xerr
		}
	}

	xerr = hostInstance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(hostproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.HostMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.HostMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			delete(mountsV1.BucketMounts, instance.GetName())
			return nil
		})
	})
	if xerr != nil {
		return xerr
	}

	return instance.Alter(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		return props.Alter(bucketproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.BucketMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.BucketMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			delete(mountsV1.ByHostID, hostInstance.GetID())
			delete(mountsV1.ByHostName, hostInstance.GetName())
			return nil
		})
	})
}

// ToProtocol returns the protocol message corresponding to Bucket fields
func (instance *bucket) ToProtocol() (*protocol.BucketResponse, fail.Error) {
	out := &protocol.BucketResponse{
		Name: instance.GetName(),
	}

	xerr := instance.Review(func(_ data.Clonable, props *serialize.JSONProperties) fail.Error {
		svc := instance.GetService()
		return props.Inspect(bucketproperty.MountsV1, func(clonable data.Clonable) fail.Error {
			mountsV1, ok := clonable.(*propertiesv1.BucketMounts)
			if !ok {
				return fail.InconsistentError("'*propertiesv1.BucketMounts' expected, '%s' provided", reflect.TypeOf(clonable).String())
			}

			out.Mounts = make([]*protocol.BucketMount, 0, len(mountsV1.ByHostID))
			for k, v := range mountsV1.ByHostID {
				hostInstance, xerr := LoadHost(svc, k)
				if xerr != nil {
					return xerr
				}

				//goland:noinspection GoDeferInLoop
				defer func(i resources.Host) { // nolint
					i.Released()
				}(hostInstance)

				out.Mounts = append(out.Mounts, &protocol.BucketMount{
					Host: &protocol.Reference{
						Id:   k,
						Name: hostInstance.GetName(),
					},
					Path: v,
				})
			}
			return nil
		})
	})
	if xerr != nil {
		return nil, xerr
	}

	return out, nil
}
