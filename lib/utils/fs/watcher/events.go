package watcher

import (
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

type events struct {
	onFolderCreation func(*Entry) error // function called when the Entry appears
	onFolderRemoval  func(*Entry) error // function called when the Entry is removed
	onFileCreation   func(*Entry) error // function called when a file is created in the Entry
	onFileRemoval    func(*Entry) error // function called when a file is removed from Entry
	onFileChange     func(*Entry) error // function called when a file is change in the Entry (content or access rights)
}

// onEvent reacts to filesystem change event
func (w *Watcher) onEvent(e fsnotify.Event) {
	// Find the entry corresponding to the name
	entry, ok := w.pathes[e.Name]
	if !ok {
		dirname := filepath.Dir(e.Name)
		entry, ok = w.pathes[dirname]
	}
	if !ok || entry == nil {
		return
	}

	// react on fsnotify event
	switch {
	case e.Op&fsnotify.Chmod == fsnotify.Chmod:
		fallthrough
	case e.Op&fsnotify.Write == fsnotify.Write:
		stat, err := os.Stat(e.Name)
		if err == nil && !stat.IsDir() {
			fd, err := os.Open(e.Name)
			if err == nil {
				_ = fd.Close()
				return
			}

			// Cannot read the file after chmod, call the handler onFileChange
			if entry.handlers.onFileChange != nil {
				err = entry.handlers.onFileChange(entry)
				if err != nil {
					logrus.Error(err)
				}
			}
		}

	case e.Op&fsnotify.Remove == fsnotify.Remove:
		stat, err := os.Stat(e.Name)
		if err == nil {
			if stat.IsDir() {
				if entry.handlers.onFolderRemoval != nil {
					err = entry.handlers.onFolderRemoval(entry)
					if err != nil {
						logrus.Error(err)
					}
				}
			} else if entry.handlers.onFileRemoval != nil {
				err = entry.handlers.onFileRemoval(entry)
				if err != nil {
					logrus.Error(err)
				}
			}
		}

	case e.Op&fsnotify.Rename == fsnotify.Rename:
		// FIXME: what to do with Rename?

	case e.Op&fsnotify.Create == fsnotify.Create:
		stat, err := os.Stat(e.Name)
		if err == nil {
			if stat.IsDir() {
				if entry.handlers.onFolderCreation != nil {
					err = entry.handlers.onFolderCreation(entry)
					if err != nil {
						logrus.Error(err)
					}
				}
			} else if entry.handlers.onFileCreation != nil {
				err = entry.handlers.onFileCreation(entry)
				if err != nil {
					logrus.Error(err)
				}
			}
		}
	}
}
