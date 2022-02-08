package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type Watcher struct {
	watched  entries // contains entries to watch into
	fathers  parents // contains parent entries where watch is wanted to react to
	fsnotify *fsnotify.Watcher
	task     concurrency.Task
	settings Settings
	pathes   entries // list of all pathes that are watched (including subfolders and parents)
}

// NewWatcher creates a new instance of Watcher
func NewWatcher(ctx context.Context, settings Settings) (*Watcher, error) {
	out := &Watcher{
		watched:  newEntries(),
		fathers:  newParents(),
		settings: settings,
		pathes:   map[string]*Entry{},
	}
	return out, nil
}

// Start starts watcher
func (w *Watcher) Start(ctx context.Context) (outerr error) {
	defer func() {
		if outerr != nil {
			w.Stop()
		}
	}()

	fsnotifyW, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}

	w.fsnotify = fsnotifyW
	w.task, err = concurrency.NewTaskWithContext(ctx)
	if err != nil {
		return err
	}

	// Adds all pathes to watch
	for _, v := range w.watched {
		err := w.addWatch(v)
		if err != nil {
			return err
		}
	}

	// Now starts task that does the watch
	_, err = w.task.Start(func(t concurrency.Task, _ concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
		return nil, w.watch(t)
	}, nil)

	return err
}

// watch is the function that receive fsnotify signals
func (w *Watcher) watch(task concurrency.Task) fail.Error {
	for {
		if task.Aborted() {
			return fail.AbortedError(nil)
		}

		select {
		case event, ok := <-w.fsnotify.Events:
			if !ok {
				return nil
			}
			w.onEvent(event)

		case err, ok := <-w.fsnotify.Errors:
			if !ok {
				return nil
			}
			logrus.Error("watcher returned an error: ", err)
		}
	}

	return nil
}

// Stop stops watcher and cleanup
func (w *Watcher) Stop() error {
	if w.task != nil {
		w.task.Abort()
		w.task.Wait()
		w.task = nil
	}

	if w.fsnotify != nil {
		w.fsnotify.Close()
		w.fsnotify = nil
	}

	w.pathes = entries{}
	return nil
}

func (w *Watcher) Add(path string, kind EntryType, recurse, watchParent bool) (*Entry, error) {
	if w == nil {
		return nil, fail.InvalidInstanceError()
	}
	if path == "" {
		return nil, fail.InvalidParameterCannotBeNilError("path")
	}
	if _, ok := w.watched[path]; ok {
		return nil, fail.DuplicateError("path '%s' is already watched")
	}

	entry, err := newEntry(path, kind, recurse, watchParent)
	if err != nil {
		return nil, err
	}

	// if watchForCreation {
	// 	fatherPath, _ := filepath.Split(path)
	// 	father, ok := w.fathers[fatherPath]
	// 	if !ok {
	// 		father, err = newParent(fatherPath)
	// 		if err != nil {
	// 			return nil, err
	// 		}
	//
	// 		w.fathers[fatherPath] = father
	// 	}
	// 	father.children[path] = struct{}{}
	// }

	w.watched[path] = entry

	return entry, nil
}

// addWatch ...
func (w *Watcher) addWatch(entry *Entry) error {
	if w == nil {
		return fail.InvalidInstanceError()
	}
	if entry == nil {
		return fail.InvalidParameterCannotBeNilError("entry")
	}

	if entry.recurse {
		return filepath.Walk(entry.path, func(walkPath string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				if _, ok := w.pathes[walkPath]; !ok {
					err = w.fsnotify.Add(walkPath)
					if err != nil {
						return err
					}

					w.pathes[walkPath] = entry
				}
			}
			return nil
		})
	}

	if entry.watchParent {
		dirname := filepath.Dir(entry.path)
		father, ok := w.fathers[dirname]
		if !ok {
			var err error
			father, err = newParent(dirname)
			if err != nil {
				return err
			}

			err = w.fsnotify.Add(father.path)
			if err != nil {
				return err
			}

			w.pathes[dirname] = father.Entry
		}
		father.children[entry.path] = struct{}{}
	}

	err := w.fsnotify.Add(entry.path)
	if err != nil {
		return err
	}

	return nil
}

// removeWatch removes watch of the Entry corresponding to parameter
func (w *Watcher) removeWatch(entry *Entry) error {
	if w == nil {
		return fail.InvalidInstanceError()
	}
	if entry == nil {
		return fail.InvalidParameterCannotBeNilError("entry")
	}

	if entry.recurse {
		return filepath.Walk(entry.path, func(walkPath string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				err = w.fsnotify.Remove(walkPath)
				if err != nil {
					return err
				}

				delete(w.pathes, walkPath)
			}
			return nil
		})
	}

	if entry.watchParent && entry.father != nil {
		if _, ok := entry.father.children[entry.path]; ok {
			dirname := entry.father.path

			// If there are more than one children attached to the parent, do not cancel the fsnotify on it
			if len(entry.father.children) == 1 {
				if _, ok := w.pathes[dirname]; !ok {
					err := w.fsnotify.Remove(dirname)
					if err != nil {
						return err
					}

					delete(w.pathes, dirname)
				}
			}

			delete(entry.father.children, entry.path)
		}
	}

	err := w.fsnotify.Remove(entry.path)
	if err != nil {
		return err
	}

	return nil
}

// Remove stops watch on a path
func (w *Watcher) Remove(path string) error {
	if w == nil {
		return fail.InvalidInstanceError()
	}
	if path == "" {
		return fail.InvalidParameterCannotBeNilError("path")
	}

	fld, ok := w.watched[path]
	if !ok {
		return fail.NotFoundError("path '%s' is not watched")
	}

	return w.removeWatch(fld)
}

// Inspect returns the Entry instance corresponding to the path
func (w Watcher) Inspect(path string) (*Entry, error) {
	if path == "" {
		return nil, fail.InvalidParameterCannotBeNilError("path")
	}

	fld, ok := w.watched[path]
	if !ok {
		return nil, fail.NotFoundError("path '%s' is not watched", path)
	}

	return fld, nil
}

// reduceFilename removes the absolute part of 'name' corresponding to folder
func (w Watcher) reduceFilename(name string) string {
	last := name
	for _, v := range w.pathes {
		if strings.HasPrefix(name, v.path) {
			reduced := strings.TrimPrefix(name, v.path)
			if len(reduced) < len(last) {
				last = reduced
			}
		}
	}
	return last
}
