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

package cache

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

func TestLockContent(t *testing.T) {
	content := &reservation{key: "content"}
	cacheEntry := newEntry(content)

	assert.EqualValues(t, uint(0), cacheEntry.LockCount())

	cacheEntry.LockContent()
	assert.EqualValues(t, uint(1), cacheEntry.LockCount())

	cacheEntry.LockContent()
	assert.EqualValues(t, uint(2), cacheEntry.LockCount())

	cacheEntry.UnlockContent()
	assert.EqualValues(t, uint(1), cacheEntry.LockCount())

	cacheEntry.UnlockContent()
	assert.EqualValues(t, uint(0), cacheEntry.LockCount())
}

func TestParallelLockContent(t *testing.T) {
	content := &reservation{key: "content"}
	cacheEntry := newEntry(content)

	task1, _ := concurrency.NewUnbreakableTask()
	task2, _ := concurrency.NewUnbreakableTask()

	_, _ = task1.Start(func(task concurrency.Task, p concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
		cacheEntry.LockContent()
		assert.Equal(t, uint(1), cacheEntry.LockCount())

		time.Sleep(time.Second)

		assert.Equal(t, uint(2), cacheEntry.LockCount())

		cacheEntry.UnlockContent()

		return nil, nil
	}, nil)

	_, _ = task2.Start(func(task concurrency.Task, p concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
		time.Sleep(time.Millisecond * 250)
		assert.Equal(t, uint(1), cacheEntry.LockCount())

		cacheEntry.LockContent()
		assert.Equal(t, uint(2), cacheEntry.LockCount())

		time.Sleep(time.Second)

		cacheEntry.UnlockContent()
		assert.Equal(t, uint(0), cacheEntry.LockCount())

		return nil, nil
	}, nil)

	_, _ = task1.Wait()
	_, _ = task2.Wait()

	assert.EqualValues(t, uint(0), cacheEntry.LockCount())
}

func makeDeadlockHappy(mdh *Cache) {
	// doing some stuff that ends up calling....
	anotherRead, err := (*mdh).GetEntry("What")
	if err != nil {
		panic(err)
	}

	theReadCt := anotherRead.Content() // Deadlock
	fmt.Printf("The deadlocked content : %v\n", theReadCt)
}

// FIXME: test Entry.lock() and Entry.unlock() in a multiple go routines situation
func TestDeadlock(t *testing.T) {
	content := &reservation{key: "content"}

	nukaCola , _ := NewCache("nuka")
	err := nukaCola.ReserveEntry("What")
	if err != nil {
		panic(err)
	}

	// between reserve and commit, someone with a reference to our cache just checks its content
	makeDeadlockHappy(&nukaCola)

	/*
	// doing some stuff that ends up calling....
	anotherRead, err := nukaCola.GetEntry("What")
	if err != nil {
		panic(err)
	}
	theReadCt := anotherRead.Content() // Deadlock
	fmt.Printf("The deadlocked content : %v\n", theReadCt)
	 */

	time.Sleep(1*time.Second)
	_, xerr := nukaCola.CommitEntry("What", content)
	if xerr != nil {
		panic(xerr)
	}

	theX, err := nukaCola.GetEntry("What")
	if err != nil {
		panic(err)
	}
	fmt.Println(theX)
}

func TestLostCache(t *testing.T) {
	content := &reservation{key: "content"}

	nukaCola , err := NewCache("nuka")
	if err != nil {
		panic(err)
	}

	err = nukaCola.ReserveEntry("What")
	if err != nil {
		panic(err)
	}

	time.Sleep(100*time.Millisecond)

	compilerHappy, fe := nukaCola.CommitEntry("What", content)
	if fe != nil {
		panic(fe)
	}

	_ = compilerHappy

	time.Sleep(1 * time.Second)

	theX, err := nukaCola.GetEntry(content.GetID())
	if err != nil {
		panic(err)
	}

	theX, err = nukaCola.GetEntry("What")
	if err != nil {
		panic(err)
	}

	fmt.Println(theX)

}

