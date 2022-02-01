/*
 * Copyright 2018-2022, CS Systemes d'Information, http://csgroup.eu
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

package cache

//go:generate minimock -o ../mocks/mock_clonable.go -i github.com/CS-SI/SafeScale/lib/utils/data/cached.MapStore

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/debug/callstack"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/sirupsen/logrus"
)

type MapStore struct {
	name     atomic.Value
	lock     sync.Mutex
	cached   map[string]*Entry
	reserved map[string]*reservation
}

// NewMapStore creates a new cache storage based on map (thread-safe)
func NewMapStore(name string) (Store, fail.Error) {
	if name == "" {
		return &MapStore{}, fail.InvalidParameterCannotBeEmptyStringError("id")
	}

	instance := &MapStore{
		cached:   map[string]*Entry{},
		reserved: map[string]*reservation{},
	}
	instance.name.Store(name)
	return instance, nil
}

func (instance *MapStore) isNull() bool {
	return instance == nil || instance.name.Load().(string) == "" || instance.cached == nil
}

// GetID satisfies interface data.Identifiable
func (instance *MapStore) GetID() string {
	return instance.name.Load().(string)
}

// GetName satisfies interface data.Identifiable
func (instance *MapStore) GetName() string {
	return instance.name.Load().(string)
}

// Entry returns a cached entry from its key
func (instance *MapStore) Entry(key string) (*Entry, fail.Error) {
	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}
	if key == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("id")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	// If key is reserved, we may have to wait reservation committed, freed or timed out
	reservation, ok := instance.reserved[key]
	if ok {
		xerr := reservation.WaitReleased()
		if xerr != nil {
			switch xerr.(type) {
			case *fail.ErrDuplicate:
				// We want the cache entry, so this error is attended, continue

			case *fail.ErrTimeout:
				derr := instance.unsafeFreeEntry(key)
				if derr != nil {
					_ = xerr.AddConsequence(derr)
				}
				return nil, fail.NotFoundError("failed to find entry '%s' in %s cache: %v", key, instance.GetName(), xerr)

			default:
				return nil, xerr
			}
		}
	}

	if ce, ok := instance.cached[key]; ok {
		return ce, nil
	}

	return nil, fail.NotFoundError("failed to find entry with key '%s' in %s cache", key, instance.GetName())
}

/*
Reserve locks an entry identified by key for update

Returns:
	nil: reservation succeeded
	*fail.ErrNotAvailable; if entry is already reserved
	*fail.ErrDuplicate: if entry is already present
*/
func (instance *MapStore) Reserve(key string, timeout time.Duration) (xerr fail.Error) {
	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if key = strings.TrimSpace(key); key == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("key")
	}
	if timeout == 0 {
		return fail.InvalidParameterError("timeout", "cannot be 0")
	}

	// If key is already reserved, we may have to wait reservation committed or freed to determine if we can effectively reserve it
	instance.lock.Lock()
	defer instance.lock.Unlock()

	if _, ok := instance.reserved[key]; ok {
		ce, ok := instance.cached[key]
		if !ok {
			return fail.InconsistentError("reserved entry '%s' in %s cache does not have a corresponding cached entry", key, instance.GetName())
		}

		reservation, ok := ce.Content().(*reservation)
		if !ok {
			// May have transitioned from reservation content to real content, first check that there is no more reservation...
			if _, ok := instance.reserved[key]; ok {
				xerr = fail.NotAvailableError("the entry '%s' of %s cache is already reserved", key, instance.GetName())
				logrus.Errorf(callstack.DecorateWith("", xerr.Error(), "", 0))
				return xerr
			}
		} else {
			xerr := reservation.WaitReleased()
			if xerr != nil {
				switch xerr.(type) {
				case *fail.ErrDuplicate:
					return xerr

				case *fail.ErrTimeout:
					derr := instance.unsafeFreeEntry(key)
					if derr != nil {
						_ = xerr.AddConsequence(derr)
					}
					// continue to reserve entry

				default:
					return xerr
				}
			}
		}
	}

	content := newReservation(key)
	content.timeout = timeout
	ce := newEntry(content)
	pce := &ce
	instance.cached[key] = pce
	instance.reserved[key] = content
	return nil
}

/*
Commit fills a previously reserved entry with content
The key retained at the end in the cached may be different to the one passed in parameter (and used previously in ReserveEntry()), because content.ID() has to be the final key.

Returns:
	nil, *fail.ErrNotFound: the cached entry identified by 'key' is not reserved
	nil, *fail.ErrNotAvailable: the content of the cached entry cannot be committed, because the content ID has changed and this new key has already been reserved
	nil, *fail.ErrDuplicate: the content of the cached entry cannot be committed, because the content ID has changed and this new key is already present in the cached
	*Entry, nil: content committed successfully

Note: if CommitEntry fails, you still have to call FreeEntry to release the reservation
*/
func (instance *MapStore) Commit(key string, content Cacheable) (ce *Entry, xerr fail.Error) {
	if instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}
	if key = strings.TrimSpace(key); key == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("key")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	if _, ok := instance.reserved[key]; !ok {
		// return nil, fail.NotFoundError("the cached entry '%s' is not reserved (may have expired)", key)
		return nil, fail.NotFoundError(callstack.DecorateWith("", fmt.Sprintf("the cache entry '%s' is not reserved (may have expired)", key), "", 0))
	}

	// content may bring new key, based on content.ID(), different from the key reserved; we have to check if this new key has not been reserved by someone else...
	var reservedEntry *reservation
	newContentKey := content.GetID()
	if newContentKey != key {
		var ok bool
		reservedEntry, ok = instance.reserved[newContentKey]
		if ok {
			return nil, fail.NotAvailableError("the cached entry '%s' in %s cache, corresponding to the new ID of the content, is reserved; content cannot be committed", newContentKey, instance.name)
		}
		if _, ok := instance.cached[content.GetID()]; ok {
			return nil, fail.DuplicateError("the cached entry '%s' in %s cache, corresponding to the new ID of the content, is already used; content cannot be committed", newContentKey, instance.name)
		}
	}
	if reservedEntry != nil {
		if reservedEntry.timeout < time.Since(reservedEntry.created) {
			// reservation has expired...
			cleanErr := fail.TimeoutError(nil, reservedEntry.timeout, "reservation of key '%s' in %s cache has expired")
			derr := instance.unsafeFreeEntry(key)
			if derr != nil {
				_ = cleanErr.AddConsequence(derr)
			}
			return nil, cleanErr
		}
	}

	// Everything is fine, we can update
	cacheEntry, ok := instance.cached[key]
	if ok {
		oldContent := cacheEntry.Content()
		r, ok := oldContent.(*reservation)
		if !ok {
			return nil, fail.InconsistentError("'*cached.reservation' expected, '%s' provided", reflect.TypeOf(oldContent).String())
		}

		// TODO: this has to be tested with a specific unit test
		err := content.AddObserver(instance)
		if err != nil {
			return nil, fail.ConvertError(err)
		}

		// Update cached entry with real content
		cacheEntry.lock.Lock()
		cacheEntry.content = data.NewImmutableKeyValue(newContentKey, content)
		cacheEntry.lock.Unlock() // nolint

		// reserved key may have to change accordingly with the ID of content
		delete(instance.cached, key)
		delete(instance.reserved, key)
		instance.cached[newContentKey] = cacheEntry

		// signal potential waiter on Entry() that reservation has been committed
		if r.committedCh != nil {
			r.committedCh <- struct{}{}
			close(r.committedCh)
		}

		return cacheEntry, nil
	}

	return nil, fail.InconsistentError("the reservation does not have a corresponding entry identified by '%s' in %s cache", key, instance.GetName())
}

// Free unlocks the cached entry and removes the reservation
// returns:
//  - nil: reservation removed
//  - *fail.ErrNotAvailable: the cached entry identified by 'key' is not reserved
//  - *fail.InconsistentError: the cached entry of the reservation should have been *cached.reservation, and is not
func (instance *MapStore) Free(key string) (xerr fail.Error) {
	if instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if key = strings.TrimSpace(key); key == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("key")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	return instance.unsafeFreeEntry(key)
}

// unsafeFreeEntry is the workforce of FreeEntry, without locking
func (instance *MapStore) unsafeFreeEntry(key string) fail.Error {
	if _, ok := instance.reserved[key]; !ok {
		return fail.NotAvailableError("the entry '%s' in cache %s is not reserved", key, instance.GetName())
	}

	var (
		ce *Entry
		ok bool
	)
	if ce, ok = instance.cached[key]; ok {
		r, ok := ce.Content().(*reservation)
		if !ok {
			return fail.InconsistentError("'*cached.reservation' expected, '%s' provided", reflect.TypeOf(ce.Content()).String())
		}

		// Cleanup key from cached and reservations
		delete(instance.cached, key)
		delete(instance.reserved, key)

		// Signal potential waiters the reservation has been freed
		if r.freedCh != nil {
			r.freedCh <- struct{}{}
			close(r.freedCh)
		}
	}

	return nil
}

const reservationTimeoutForAddition = 5 * time.Second

// Add adds a content in cache
func (instance *MapStore) Add(content Cacheable) (_ *Entry, ferr fail.Error) {
	if instance == nil {
		return nil, fail.InvalidInstanceError()
	}
	if content == nil {
		return nil, fail.InvalidParameterCannotBeNilError("content")
	}

	// instance.lock.Lock()
	// defer instance.lock.Unlock()

	id := content.GetID()
	xerr := instance.Reserve(id, reservationTimeoutForAddition)
	if xerr != nil {
		return nil, xerr
	}

	defer func() {
		if ferr != nil {
			derr := instance.Free(id)
			if derr != nil {
				_ = ferr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to free entry '%s' in cache %s", id, instance.GetName()))
			}
		}
	}()

	cacheEntry, xerr := instance.Commit(id, content)
	if xerr != nil {
		return nil, xerr
	}

	return cacheEntry, nil
}

// SignalChange tells the cached entry something has been changed in the content
func (instance *MapStore) SignalChange(key string) {
	if instance == nil {
		return
	}

	if key == "" {
		return
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	if ce, ok := instance.cached[key]; ok {
		ce.lock.Lock()
		defer ce.lock.Unlock()

		ce.lastUpdated = time.Now()
	}
}

// MarkAsFreed tells the cached to unlock content (decrementing the counter of uses)
func (instance *MapStore) MarkAsFreed(id string) {
	if instance == nil {
		return
	}

	if id == "" {
		return
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	ce, ok := instance.cached[id]
	if ok {
		ce.UnlockContent()
	}
}

// MarkAsDeleted tells the cached entry to be considered as deleted
func (instance *MapStore) MarkAsDeleted(key string) {
	if instance == nil {
		return
	}

	if key == "" {
		return
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	delete(instance.cached, key)
}
