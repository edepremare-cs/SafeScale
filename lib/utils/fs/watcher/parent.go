package watcher

import (
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

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
