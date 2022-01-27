package cache

import (
	"sync"
	"time"

	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// SingleMemoryCache proposes a sinmple way to use a cache from a mapcachestore
type SingleMemoryCache struct {
	store Store
	lock  sync.Mutex
}

const (
	OptionOnMissKeyword        = "on_miss"
	OptionOnMissTimeoutKeyword = "on_miss_timeout"
)

// MissEventOption returns []data.ImmutableKeyValue options to use on cache miss with timeout
func MissEventOption(fn func() (Cacheable, fail.Error), timeout time.Duration) []data.ImmutableKeyValue {
	if timeout <= 0 {
		return []data.ImmutableKeyValue{
			data.NewImmutableKeyValue(OptionOnMissKeyword, func() (Cacheable, fail.Error) {
				return nil, fail.InvalidRequestError("invalid timeout for function provided to react on cache miss event: cannot be less or equal to 0")
			}),
			data.NewImmutableKeyValue(OptionOnMissTimeoutKeyword, timeout),
		}
	}

	if fn != nil {
		return []data.ImmutableKeyValue{
			data.NewImmutableKeyValue(OptionOnMissKeyword, fn),
			data.NewImmutableKeyValue(OptionOnMissTimeoutKeyword, timeout),
		}
	}

	return []data.ImmutableKeyValue{
		data.NewImmutableKeyValue(OptionOnMissKeyword, func() (Cacheable, fail.Error) {
			return nil, fail.InvalidRequestError("invalid function provided to react on cache miss event: cannot be nil")
		}),
		data.NewImmutableKeyValue(OptionOnMissTimeoutKeyword, timeout),
	}
}

// NewSingleMemoryCache initializes a new instance of SingleMemoryCache
func NewSingleMemoryCache(name string) (*SingleMemoryCache, fail.Error) {
	if name == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("name")
	}

	storeInstance, xerr := NewMapStore(name)
	if xerr != nil {
		return &SingleMemoryCache{}, xerr
	}

	rc := &SingleMemoryCache{
		store: storeInstance,
	}
	return rc, nil
}

// isNull tells if rc is a null value of *ResourceCache
func (instance *SingleMemoryCache) isNull() bool {
	return instance == nil || instance.store == nil
}

// Get returns the content associated with key
func (instance *SingleMemoryCache) Get(key string, options ...data.ImmutableKeyValue) (ce *Entry, xerr fail.Error) {
	if instance == nil || instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}
	if key == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("key")
	}

	ce, found := instance.loadEntry(key)
	if found {
		return ce, nil
	}

	// We have a cache miss, check if we have a function to get the missing content
	if len(options) > 0 {
		var (
			onMissFunc    func() (Cacheable, fail.Error)
			onMissTimeout time.Duration
		)
		for _, v := range options {
			switch v.Key() {
			case OptionOnMissKeyword:
				var ok bool
				onMissFunc, ok = v.Value().(func() (Cacheable, fail.Error))
				if !ok {
					return nil, fail.InconsistentError("expected callback for '%s' event must be of type 'func() (cache.Cacheable, fail.Error)'; provided type: %v", OptionOnMissKeyword, v.Value())
				}
			case OptionOnMissTimeoutKeyword:
				var ok bool
				onMissTimeout, ok = v.Value().(time.Duration)
				if !ok {
					return nil, fail.InconsistentError("expected value for '%s' event must be of type 'time.Duration'; provided type: %v", OptionOnMissKeyword, v.Value())
				}
			default:
			}
		}

		if onMissFunc != nil {
			// Sets a default reserve timeout
			if onMissTimeout <= 0 {
				onMissTimeout = temporal.DefaultDelay()
			}

			xerr := instance.ReserveEntry(key, onMissTimeout)
			if xerr != nil {
				switch xerr.(type) {
				case *fail.ErrDuplicate:
					// Search in the cache by ID
					ce, xerr = instance.store.Entry(key)
					if xerr != nil {
						return nil, xerr
					}

					return ce, nil

				default:
					return nil, xerr
				}
			}

			var content Cacheable
			if content, xerr = onMissFunc(); xerr == nil {
				ce, xerr = instance.CommitEntry(key, content)
			}
			if xerr != nil {
				if derr := instance.FreeEntry(key); derr != nil {
					_ = xerr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to free cache entry with key '%s'", key))
				}
				return nil, xerr
			}
			return ce, nil
		}
	}

	return nil, fail.NotFoundError("failed to find cache entry for key '%s', and does not know how to fill the miss", key)
}

// loadEntry returns the entry corresponding to the key if it exists
// returns:
// - *cache.Entry, true: if key is found
// - nil, false: if key is not found
func (instance *SingleMemoryCache) loadEntry(key string) (*Entry, bool) {
	instance.lock.Lock()
	defer instance.lock.Unlock()

	ce, xerr := instance.store.Entry(key)
	if xerr != nil {
		return nil, false
	}

	return ce, true

}

// ReserveEntry sets a cache entry to reserve the key and returns the Entry associated
func (instance *SingleMemoryCache) ReserveEntry(key string, timeout time.Duration) fail.Error {
	if instance == nil || instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if key == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("key")
	}
	if timeout <= 0 {
		return fail.InvalidParameterError("timeout", "cannot be less or equal to 0")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	return instance.store.Reserve(key, timeout)
}

// CommitEntry confirms the entry in the cache with the content passed as parameter
func (instance *SingleMemoryCache) CommitEntry(key string, content Cacheable) (ce *Entry, xerr fail.Error) {
	if instance == nil || instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}
	if key == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("key")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	ce, xerr = instance.store.Commit(key, content)
	if xerr != nil {
		return nil, xerr
	}

	return ce, nil
}

// FreeEntry removes the reservation in cache
func (instance *SingleMemoryCache) FreeEntry(key string) fail.Error {
	if instance == nil || instance.isNull() {
		return fail.InvalidInstanceError()
	}
	if key == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("key")
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	return instance.store.Free(key)
}

// AddEntry ...
func (instance *SingleMemoryCache) AddEntry(content Cacheable) (ce *Entry, xerr fail.Error) {
	if instance == nil || instance.isNull() {
		return nil, fail.InvalidInstanceError()
	}

	instance.lock.Lock()
	defer instance.lock.Unlock()

	ce, xerr = instance.store.Add(content)
	if xerr != nil {
		return nil, xerr
	}

	return ce, nil
}
