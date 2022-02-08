package watcher

import (
	"os"

	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

type EntryType bool

const (
	FolderTypeEntry = true
	FileTypeEntry   = false

	WatchForCreation      = true
	DoNotWatchForCreation = false
)

// Entry describes a file system entry that has to be watch for events
type Entry struct {
	handlers    events
	path        string
	kind        EntryType
	father      *parent // if watchParent == true, contains the parent instance
	watchParent bool    // if == true, watch parent kind for entry creation
	recurse     bool    // if == true, watch kind and subfolders
	active      bool    // if == true, the watch is activa
}

// newEntry ...
func newEntry(path string, kind EntryType, recurse, watchParent bool) (*Entry, error) {
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
		if watchParent == DoNotWatchForCreation {
			return nil, fail.InvalidRequestError("cannot watch non-existent entry if not explicitly asked for creation watch")
		}
	}

	out := Entry{
		path:        path,
		kind:        kind,
		recurse:     recurse,
		watchParent: watchParent,
	}

	// if watchParent {
	// 	var father *parent
	// 	dirname := filepath.Dir(path)
	// 	if dirname != "" {
	// 		father, err = newParent(dirname)
	// 		if err != nil {
	// 			return nil, err
	// 		}
	// 	}
	//
	// 	father.children[path] = struct{}{}
	// 	out.father = father
	// }
	return &out, nil
}

func (f *Entry) SetCallbackOnFolderCreation(cb func(*Entry) error) error {
	if f == nil {
		return fail.InvalidInstanceError()
	}
	if !f.kind {
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
	if !f.kind {
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
