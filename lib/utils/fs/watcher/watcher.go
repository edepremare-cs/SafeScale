package watcher

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type EntryType bool

const (
	FolderTypeEntry = true
	FileTypeEntry   = false

	WatchForCreation      = true
	DoNotWatchForCreation = false
)

type events struct {
	onFolderCreation func(*Entry) error // function called when the Entry appears
	onFolderRemoval  func(*Entry) error // function called when the Entry is removed
	onFileCreation   func(*Entry) error // function called when a file is created in the Entry
	onFileRemoval    func(*Entry) error // function called when a file is removed from Entry
	onFileChange     func(*Entry) error // function called when a file is change in the Entry (content or access rights)
}

// Entry describes a file system entry that has to be watch for events
type Entry struct {
	handlers    events
	path        string
	folder      EntryType
	watchParent bool // if == true, watch parent folder for entry creation
	recurse     bool // if == true, watch folder and subfolders
	active      bool // if == true, the watch is activa
}

// newEntry ...
func newEntry(path string, kind EntryType, recurse, watchForCreation bool) (*Entry, error) {
	if path == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("path")
	}

	fi, err := os.Stat(path)
	if err == nil {
		if fi.IsDir() {
			if kind == FileTypeEntry {
				return nil, fail.InvalidRequestError("cannot watch a file as if it's a folder")
			}
		} else {
			// It's a file, cannot recurse on a file...
			if recurse {
				return nil, fail.InvalidRequestError("cannot watch recursively on a file")
			}
		}
	} else {
		// path not found, no sense if watchForCreation is false
		if watchForCreation == DoNotWatchForCreation {
			return nil, fail.InvalidRequestError("cannot watch non-existent entry if not explicitely asked for creation watch")
		}
	}

	out := Entry{
		path:        path,
		folder:      kind,
		recurse:     recurse,
		watchParent: watchForCreation,
	}
	return &out, nil
}

func (f *Entry) SetCallbackOnFolderCreation(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if !f.folder {
		return fail.InvalidRequestError("cannot set callback on folder creation event for a file")
	}
	if cb == nil {
		return fail.InvalidParameterCannotBeNilError("cb")
	}

	f.handlers.onFolderCreation = cb
	return nil
}

func (f *Entry) SetCallbackOnFolderRemoval(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if !f.folder {
		return fail.InvalidRequestError("cannot set callback on folder creation event for a file")
	}
	if cb == nil {
		return fail.InvalidParameterCannotBeNilError("cb")
	}

	f.handlers.onFolderRemoval = cb
	return nil
}

func (f *Entry) SetCallbackOnFileCreation(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if cb == nil {
		return fail.InvalidParameterCannotBeNilError("cb")
	}

	f.handlers.onFileCreation = cb
	return nil
}

func (f *Entry) SetCallbackOnFileRemoval(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if cb == nil {
		return fail.InvalidParameterCannotBeNilError("cb")
	}

	f.handlers.onFileRemoval = cb
	return nil
}

func (f *Entry) SetCallbackOnFileChange(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if cb == nil {
		return fail.InvalidParameterCannotBeNilError("cb")
	}

	f.handlers.onFileChange = cb
	return nil
}

type entries map[string]*Entry

func newEntries() entries {
	return entries{}
}

// parent describes parent folder that will be monitored to react on events regarding any entry in 'children' (and ignore anything else)
type parent struct {
	*Entry
	children map[string]struct{}
}

func newParent(path string) (*parent, error) {
	if path == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("path")
	}

	fld, err := newEntry(path, FolderTypeEntry, false, DoNotWatchForCreation)
	if err != nil {
		return nil, err
	}

	out := parent{
		Entry:    fld,
		children: make(map[string]struct{}),
	}
	return &out, nil
}

type parents map[string]*parent

func newParents() parents {
	return parents{}
}

type Watcher struct {
	watched  entries // contains entries to watch into
	fathers  parents // contains parent entries where watch is wanted to react to
	fsnotify *fsnotify.Watcher
	task     concurrency.Task
}

// NewWatcher creates a new instance of Watcher
func NewWatcher(ctx context.Context) (*Watcher, error) {
	fsnotifyW, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	out := &Watcher{
		watched:  newEntries(),
		fathers:  newParents(),
		fsnotify: fsnotifyW,
	}

	out.task, err = concurrency.NewTaskWithContext(ctx)
	if err != nil {
		return nil, err
	}
	_, err = out.task.Start(func(_ concurrency.Task, _ concurrency.TaskParameters) (concurrency.TaskResult, fail.Error) {
		return nil, out.watch()
	}, nil)

	return out, nil
}

// Close stops watcher
func (w *Watcher) Close() {
	w.task.Abort()
	w.task.Wait()

	// FIXME: disable all fsnotify
}

func (w *Watcher) Add(path string, kind EntryType, recurse, watchForCreation bool) (*Entry, error) {
	if w == nil {
		return nil, fail.InvalidInstanceError()
	}
	if path == "" {
		return nil, fail.InvalidParameterCannotBeNilError("path")
	}
	if _, ok := w.watched[path]; ok {
		return nil, fail.DuplicateError("path '%s' is already watched")
	}

	entry, err := newEntry(path, kind, recurse, watchForCreation)
	if err != nil {
		return nil, err
	}

	if watchForCreation {
		fatherPath, _ := filepath.Split(path)
		father, ok := w.fathers[fatherPath]
		if !ok {
			father, err = newParent(fatherPath)
			if err != nil {
				return nil, err
			}

			w.fathers[fatherPath] = father
		}
		father.children[path] = struct{}{}
	}

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
				err = w.fsnotify.Add(walkPath)
				if err != nil {
					return err
				}
			}
			return nil
		})
	}

	// FIXME: deal with parent watching...
	err := w.fsnotify.Add(entry.path)
	if err != nil {
		return err
	}

	return nil
}

// removeWatch adds watch of the Entry corresponding to parameter
func (w *Watcher) removeWatch(fld *Entry) error {
	if w == nil {
		return fail.InvalidInstanceError()
	}
	if fld == nil {
		return fail.InvalidParameterCannotBeNilError()
	}

	if fld.recurse {
		return filepath.Walk(fld.path, func(walkPath string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if fi.IsDir() {
				if err = w.fsnotify.Remove(walkPath); err != nil {
					return err
				}
			}
			return nil
		})
	}

	err := w.fsnotify.Remove(fld.path)
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

// watchFeatureFileFolders watches entries that may contain Feature Files and react to changes (invalidating cache entry
// already loaded)
func (w *Watcher) watch() fail.Error {
	done := make(chan bool)
	go func() {
		for {
			select {
			case event, ok := <-w.fsnotify.Events:
				if !ok {
					return
				}
				onFeatureFileEvent(w, event)

			case err, ok := <-w.fsnotify.Errors:
				if !ok {
					return
				}
				logrus.Error("Feature File watcher returned an error: ", err)
			}
		}
	}()

	<-done
	return nil
}

// onFeatureFileEvent reacts to filesystem change event
func onFeatureFileEvent(w *Watcher, e fsnotify.Event) {
	switch {
	case e.Op&fsnotify.Chmod == fsnotify.Chmod:
		fallthrough
	case e.Op&fsnotify.Remove == fsnotify.Remove:
		fallthrough
	case e.Op&fsnotify.Rename == fsnotify.Rename:
		fallthrough
	case e.Op&fsnotify.Write == fsnotify.Write:
		relativeName := reduceFilename(e.Name)
		stat, err := os.Stat(e.Name)
		if err == nil {
			if stat.IsDir() {
				// If the fs object is a folder, do nothing more
				return
			}

			// If the fs object is a file and is still readable, do nothing more
			if e.Op&fsnotify.Chmod == fsnotify.Chmod {
				fd, err := os.Open(e.Name)
				if err == nil {
					_ = fd.Close()
					return
				}
			}
		}

		// From here, we need to invalidate cache entry, either because content has changed or file have been removed/renamed or updated
		featureName := relativeName
		if strings.HasSuffix(relativeName, ".yaml") {
			featureName = strings.TrimSuffix(relativeName, ".yaml")
		}
		if strings.HasSuffix(relativeName, ".yml") {
			featureName = strings.TrimSuffix(relativeName, ".yml")
		}
		if len(featureName) != len(relativeName) {
			featureName = strings.TrimPrefix(featureName, "/")
			_, xerr := featureFileController.cache.Get(featureName)
			if xerr == nil {
				xerr = featureFileController.cache.FreeEntry(featureName)
			}
			if xerr != nil {
				switch xerr.(type) {
				case *fail.ErrNotFound:
					// No cache corresponding, ignore the error
					debug.IgnoreError(xerr)
				default:
					// otherwise, log it
					logrus.Error(xerr)
				}
			}
		}

	case e.Op&fsnotify.Create == fsnotify.Create:
		// If the object created is a path, add it to RWatcher (if it's a file, nothing more to do, cache miss will
		// do the necessary in time
		fi, err := os.Stat(e.Name)
		if err == nil && fi.IsDir() {
			w.AddRecursive(e.Name)
		}
	}
}

// reduceFilename removes the absolute part of 'name' corresponding to folder
func reduceFilename(name string) string {
	last := name
	for _, v := range featureFileFolders {
		if strings.HasPrefix(name, v) {
			reduced := strings.TrimPrefix(name, v)
			if len(reduced) < len(last) {
				last = reduced
			}
		}
	}
	return last
}

func init() {
	var xerr fail.Error
	featureFileController.cache, xerr = cache.NewSingleMemoryCache(featureFileKind)
	if xerr != nil {
		panic(xerr.Error())
	}

	// Starts go routine watching changes in Feature File entries
	go watchFeatureFileFolders()
}
