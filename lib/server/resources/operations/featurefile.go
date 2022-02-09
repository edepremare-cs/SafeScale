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

package operations

import (
	"io/fs"
	"io/ioutil"
	"os"
	"strings"
	"sync"

	"github.com/farmergreg/rfsnotify"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
	"gopkg.in/fsnotify.v1"

	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/data/observer"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/callstack"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

const (
	featureFileKind = "featurefile"
)

type featureFileSingleton struct {
	cache *cache.SingleMemoryCache
}

var (
	featureFileController featureFileSingleton
	featureFileFolders    = []string{
		"$HOME/.safescale/features",
		"$HOME/.config/safescale/features",
		"/etc/safescale/features",
	}
	cwdFeatureFileFolders = []string{
		".",
		"./features",
		"./.safescale/features",
	}
)

// FeatureFile contains the information about an installable Feature
type FeatureFile struct {
	displayName               string                       // is the name of the feature
	fileName                  string                       // is the name of the specification file
	displayFileName           string                       // is the 'pretty' name of the specification file
	embedded                  bool                         // tells if the Feature is embedded in SafeScale
	specs                     *viper.Viper                 // is the Viper instance containing Feature file content
	parameters                map[string]FeatureParameter  // contains all the parameters defined in Feature file
	versionControl            map[string]string            // contains the templated bash code to determine version of a component used in the FeatureFile
	observersLock             *sync.RWMutex                // lock to access field 'observers'
	observers                 map[string]observer.Observer // contains the Observers of the FeatureFile
	dependencies              map[string]struct{}          // contains the list of features required to install the Feature described by the file
	clusterSizingRequirements map[string]interface{}       // contains the cluster sizing requirements to allow install
	installers                map[string]interface{}
}

func newFeatureFile() *FeatureFile {
	return &FeatureFile{
		parameters:     map[string]FeatureParameter{},
		versionControl: map[string]string{},
		observersLock:  &sync.RWMutex{},
		observers:      map[string]observer.Observer{},
	}
}

// IsNull tells if the instance represents a null value
func (ff *FeatureFile) IsNull() bool {
	return ff == nil || ff.displayName == ""
}

// Clone ...
// satisfies interface data.Clonable
func (ff *FeatureFile) Clone() data.Clonable {
	return newFeatureFile().Replace(ff)
}

// Replace ...
// satisfies interface data.Clonable
// may panic
func (ff *FeatureFile) Replace(p data.Clonable) data.Clonable {
	// Do not test with IsNull(), it's allowed to clone a null value...
	if ff == nil || p == nil {
		return ff
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*FeatureFile) // nolint
	// VPL: Not used yet, need to think if we should return an error or panic, or something else
	// src, ok := p.(*Feature)
	// if !ok {
	// 	panic("failed to cast p to '*Feature'")
	// }
	ff.displayName = src.displayName
	ff.fileName = src.fileName
	ff.displayFileName = src.displayFileName
	ff.specs = src.specs // Note: using same pointer here is wanted; do not raise an alert in UT on this
	ff.embedded = src.embedded
	for k, v := range src.observers {
		ff.observers[k] = v
	}
	return ff
}

// GetName returns the display name of the Feature, with error handling
func (ff *FeatureFile) GetName() string {
	if ff.IsNull() {
		return ""
	}

	return ff.displayName
}

// GetID ...
func (ff *FeatureFile) GetID() string {
	return ff.GetName()
}

// Filename returns the filename of the Feature definition, with error handling
func (ff *FeatureFile) Filename() string {
	if ff.IsNull() {
		return ""
	}

	return ff.fileName
}

// DisplayFilename returns the filename of the Feature definition, beautifulled, with error handling
func (ff *FeatureFile) DisplayFilename() string {
	if ff.IsNull() {
		return ""
	}
	return ff.displayFileName
}

// Specs returns a copy of the spec file (we don't want external use to modify Feature.specs)
func (ff *FeatureFile) Specs() *viper.Viper {
	if ff.IsNull() {
		return &viper.Viper{}
	}

	roSpecs := *(ff.specs)
	return &roSpecs
}

// Released is used to tell cache that the instance has been used and will not be anymore.
// Helps the cache handler to know when a cached item can be removed from cache (if needed)
// Note: Does nothing for now, prepared for future use
// satisfies interface data.Cacheable
func (ff *FeatureFile) Released() {
	if ff == nil || ff.IsNull() {
		logrus.Errorf(callstack.DecorateWith("", "Released called on an invalid instance", "cannot be nil or null value", 0))
		return
	}

	ff.observersLock.RLock()
	defer ff.observersLock.RUnlock()

	for _, v := range ff.observers {
		v.MarkAsFreed(ff.displayName)
	}
}

// Destroyed is used to tell cache that the instance has been deleted and MUST be removed from cache.
// Note: Does nothing for now, prepared for future use
// satisfies interface data.Cacheable
func (ff *FeatureFile) Destroyed() {
	if ff == nil || ff.IsNull() {
		logrus.Warnf("Destroyed called on an invalid instance")
		return
	}

	ff.observersLock.RLock()
	defer ff.observersLock.RUnlock()

	for _, v := range ff.observers {
		v.MarkAsDeleted(ff.displayName)
	}
}

// AddObserver ...
// satisfies interface data.Observable
func (ff *FeatureFile) AddObserver(o observer.Observer) error {
	if ff == nil || ff.IsNull() {
		return fail.InvalidInstanceError()
	}
	if o == nil {
		return fail.InvalidParameterError("o", "cannot be nil")
	}

	ff.observersLock.Lock()
	defer ff.observersLock.Unlock()

	if pre, ok := ff.observers[o.GetID()]; ok {
		if pre == o {
			return fail.DuplicateError("there is already an Observer identified by '%s'", o.GetID())
		}
		return nil
	}

	ff.observers[o.GetID()] = o
	return nil
}

// NotifyObservers sends a signal to all registered Observers to notify change
// Satisfies interface data.Observable
func (ff *FeatureFile) NotifyObservers() error {
	if ff == nil || ff.IsNull() {
		return fail.InvalidInstanceError()
	}

	ff.observersLock.RLock()
	defer ff.observersLock.RUnlock()

	for _, v := range ff.observers {
		v.SignalChange(ff.displayName)
	}
	return nil
}

// RemoveObserver ...
func (ff *FeatureFile) RemoveObserver(name string) error {
	if ff == nil || ff.IsNull() {
		return fail.InvalidInstanceError()
	}
	if name == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("name")
	}

	ff.observersLock.Lock()
	defer ff.observersLock.Unlock()

	delete(ff.observers, name)
	return nil
}

// LoadFeatureFile searches for a spec file named 'name' and initializes a new FeatureFile object
// with its content
// 'xerr' may contain:
//    - nil: everything worked as expected
//    - fail.ErrNotFound: no FeatureFile is found with the name
//    - fail.ErrSyntax: FeatureFile contains syntax error
func LoadFeatureFile(svc iaas.Service, name string, embeddedOnly bool) (_ *FeatureFile, xerr fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}
	if name == "" {
		return nil, fail.InvalidParameterError("name", "cannot be empty string")
	}

	cacheOptions := cache.MissEventOption(
		func() (cache.Cacheable, fail.Error) { return onFeatureFileCacheMiss(svc, name, embeddedOnly) },
		svc.Timings().MetadataTimeout(),
	)
	ce, xerr := featureFileController.cache.Get(name, cacheOptions...)
	if xerr != nil {
		logrus.Error(callstack.DecorateWith("", xerr.Error(), "", 0))
		switch xerr.(type) {
		case *fail.ErrNotFound:
			return nil, fail.NotFoundError("failed to find Feature '%s'", name)
		default:
			return nil, xerr
		}
	}

	featureFileInstance, ok := ce.Content().(*FeatureFile)
	if !ok {
		return nil, fail.InconsistentError("cache content for key '%s' is not a resources.Feature", name)
	}
	if featureFileInstance == nil {
		return nil, fail.InconsistentError("nil value found in Feature cache for key '%s'", name)
	}
	_ = ce.LockContent()
	defer func() {
		_ = ce.UnlockContent()
	}()

	return featureFileInstance, nil
}

// onFeatureFileCacheMiss is called when host 'ref' is not found in cache
func onFeatureFileCacheMiss(_ iaas.Service, name string, embeddedOnly bool) (cache.Cacheable, fail.Error) {
	var (
		newInstance *FeatureFile
		xerr        fail.Error
	)

	if embeddedOnly {
		newInstance, xerr = findEmbeddedFeatureFile(name)
		if xerr != nil {
			return nil, xerr
		}

		logrus.Tracef("Loaded Feature '%s' (%s)", newInstance.DisplayFilename(), newInstance.Filename())
	} else {
		v := viper.New()
		setViperConfigPathes(v)
		v.SetConfigName(name)
		err := v.ReadInConfig()
		err = debug.InjectPlannedError(err)
		if err != nil {
			switch err.(type) {
			case viper.ConfigFileNotFoundError:
				newInstance, xerr = findEmbeddedFeatureFile(name)
				if xerr != nil {
					return nil, xerr
				}

			default:
				return nil, fail.SyntaxError("failed to read the specification file of Feature called '%s': %s", name, err.Error())
			}
		} else {
			if !v.IsSet("feature") {
				return nil, fail.SyntaxError("missing keyword 'feature' at the beginning of the file")
			}

			newInstance = &FeatureFile{
				fileName:        v.ConfigFileUsed(),
				displayFileName: v.ConfigFileUsed(),
				displayName:     name,
				specs:           v,
				observersLock:   &sync.RWMutex{},
			}
		}

		logrus.Tracef("Loaded Feature '%s' (%s)", newInstance.DisplayFilename(), newInstance.Filename())

		// if we can log the sha256 of the feature, do it
		filename := v.ConfigFileUsed()
		if filename != "" {
			content, err := ioutil.ReadFile(filename)
			if err != nil {
				return nil, fail.ConvertError(err)
			}

			logrus.Tracef("Loaded Feature '%s' SHA256:%s", name, getSHA256Hash(string(content)))
		}
	}

	xerr = newInstance.Parse()
	if xerr != nil {
		return nil, xerr
	}

	return newInstance, nil
}

// findEmbeddedFeatureFile returns the instance of the embedded feature called 'name', if it exists
func findEmbeddedFeatureFile(name string) (*FeatureFile, fail.Error) {
	if _, ok := allEmbeddedFeaturesMap[name]; !ok {
		return nil, fail.NotFoundError("failed to find an embedded Feature named '%s'", name)
	}

	newInstance, ok := allEmbeddedFeaturesMap[name].Clone().(*FeatureFile)
	if !ok {
		return nil, fail.NewError("embedded feature should be a *Feature")
	}

	newInstance.displayFileName = name + ".yml [embedded]"
	return newInstance, nil
}

// Parse reads the content of the file, init parameter list and signals errors
func (ff *FeatureFile) Parse() fail.Error {
	if ff.IsNull() {
		return fail.InvalidInstanceError()
	}

	xerr := ff.parseFeatureRequirements()
	if xerr != nil {
		return xerr
	}

	xerr = ff.parseParameters()
	if xerr != nil {
		return xerr
	}

	xerr = ff.parseInstallers()
	if xerr != nil {
		return xerr
	}

	return nil
}

// parseFeatureRequirements parses the dependencies key
func (ff *FeatureFile) parseFeatureRequirements() fail.Error {
	const yamlRootKey = "feature.dependencies.features"

	if ff.specs.IsSet(yamlRootKey) {
		fields := ff.specs.GetStringSlice(yamlRootKey)
		out := make(map[string]struct{}, len(fields))
		for _, r := range fields {
			out[r] = struct{}{}
		}

		ff.dependencies = out
	} else {
		ff.dependencies = map[string]struct{}{}
	}

	return nil
}

// parseClusterSizingRequirements reads 'feature.dependencies.clusterSizing' from file content
func (ff *FeatureFile) parseClusterSizingRequirements() fail.Error {
	const yamlRootKey = "feature.dependencies.clusterSizing"

	if !ff.specs.IsSet(yamlRootKey) {
		ff.clusterSizingRequirements = map[string]interface{}{}
		return nil
	}

	ff.clusterSizingRequirements = ff.specs.GetStringMap(yamlRootKey)
	return nil
}

// parseParameters parses the parameters and populates 'FeatureFile.parameters'
func (ff *FeatureFile) parseParameters() fail.Error {
	const (
		yamlRootKey    = "feature.features"
		nameKey        = "name"
		descriptionKey = "description"
		valueKey       = "value"
		controlKey     = "control"
	)

	if ff.specs.IsSet(yamlRootKey) {
		params, ok := ff.specs.Get(yamlRootKey).([]interface{})
		if !ok {
			return fail.SyntaxError("unsupported content for keyword '%s': must be a list of string or struct", yamlRootKey)
		}

		for k, p := range params {
			switch p := p.(type) {
			case string:
				if p == "" {
					continue
				}

				splitted := strings.Split(p, "=")
				name := splitted[0]
				hasDefaultValue := len(splitted) > 1
				var defaultValue string
				if hasDefaultValue {
					defaultValue = splitted[1]
				}
				newParam, xerr := NewFeatureParameter(name, "", hasDefaultValue, defaultValue, false, "")
				if xerr != nil {
					return xerr
				}

				ff.parameters[name] = newParam

			case map[interface{}]interface{}:
				casted := data.ToMapStringOfString(p)
				name, ok := casted[nameKey]
				if !ok {
					return fail.SyntaxError("missing 'name' field in entry #%d of keyword 'feature.parameters'", k+1)
				}
				if strings.ContainsAny(name, "=:") {
					return fail.SyntaxError("field 'name' cannot contain '=' or ':' in entry #%d", k+1)
				}

				description, _ := casted[descriptionKey] // nolint
				value, hasDefaultValue := casted[valueKey]
				valueControlCode, hasValueControl := casted[controlKey]
				newParam, xerr := NewFeatureParameter(name, description, hasDefaultValue, value, hasValueControl, valueControlCode)
				if xerr != nil {
					return xerr
				}
				ff.parameters[name] = newParam

			default:
				return fail.SyntaxError("unsupported content for keyword 'feature.parameters': must be a list of string or struct")
			}
		}
	}

	return nil
}

// parseInstallers reads 'feature.install' keyword and split up each triplet (installer method/action/step)
func (ff *FeatureFile) parseInstallers() fail.Error {
	const yamlRootKey = "feature.install"

	w := ff.specs.GetStringMap(yamlRootKey)
	out := make(map[string]interface{}, len(w))
	for k, v := range w {
		out[k] = v
	}
	ff.installers = out

	// Does nothing yet, need to review step handling to differentiate a step definition and a step realization
	return nil
}

// getDependencies returns a copy of dependencies defined in FeatureFile
func (ff FeatureFile) getDependencies() map[string]struct{} {
	out := make(map[string]struct{}, len(ff.dependencies))
	for k := range ff.dependencies {
		out[k] = struct{}{}
	}
	return out
}

// getClusterSizingRequirements returns the list of cluster sizing requirements by cluster flavor
func (ff FeatureFile) getClusterSizingRequirements() map[string]interface{} {
	out := make(map[string]interface{}, len(ff.clusterSizingRequirements))
	for k, v := range ff.clusterSizingRequirements {
		out[k] = v
	}
	return out
}

// getClusterSizingRequirementsForFlavor returns the list of cluster sizing requirements of a specific cluster flavor
func (ff FeatureFile) getClusterSizingRequirementsForFlavor(flavor string) map[string]interface{} {
	if flavor != "" {
		anon, ok := ff.clusterSizingRequirements[flavor]
		if ok {
			sizing, ok := anon.(map[string]interface{})
			if ok {
				out := make(map[string]interface{}, len(sizing))
				for k, v := range ff.clusterSizingRequirements {
					out[k] = v
				}
				return out
			}
		}
	}

	return nil
}

// setViperConfgigPathes ...
func setViperConfigPathes(v *viper.Viper) {
	if v != nil {
		for _, i := range featureFileFolders {
			v.AddConfigPath(i)
		}
	}
}

// watchFeatureFileFolders watches folders that may contain Feature Files and react to changes (invalidating cache entry
// already loaded)
func watchFeatureFileFolders() error {
	watcher, err := rfsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = watcher.Close() }()

	done := make(chan bool)
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				onFeatureFileEvent(watcher, event)

			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logrus.Error("Feature File watcher returned an error: ", err)
			}
		}
	}()

	// get current dir
	cwd, _ := os.Getwd()

	// get homedir
	home := os.Getenv("HOME")
	if home == "" {
		home, _ = os.UserHomeDir()
	}

	for k := range featureFileFolders {
		if strings.HasPrefix(featureFileFolders[k], "$HOME") {
			if home != "" {
				featureFileFolders[k] = home + strings.TrimPrefix(featureFileFolders[k], "$HOME")
			} else {
				continue
			}
		} else if strings.HasPrefix(featureFileFolders[k], ".") {
			if cwd != "" {
				featureFileFolders[k] = cwd + strings.TrimPrefix(featureFileFolders[k], ".")
			} else {
				continue
			}
		}

		err = addPathToWatch(watcher, featureFileFolders[k])
		if err != nil {
			return err
		}
	}

	// FIXME: disabled for now, need to add a flag or configuration option to make this part optional
	// for k := range cwdFeatureFileFolders {
	// 	if strings.HasPrefix(cwdFeatureFileFolders[k], "$HOME") {
	// 		if home != "" {
	// 			cwdFeatureFileFolders[k] = home + strings.TrimPrefix(cwdFeatureFileFolders[k], "$HOME")
	// 		} else {
	// 			continue
	// 		}
	// 	} else if strings.HasPrefix(cwdFeatureFileFolders[k], ".") {
	// 		if cwd != "" {
	// 			cwdFeatureFileFolders[k] = cwd + strings.TrimPrefix(cwdFeatureFileFolders[k], ".")
	// 		} else {
	// 			continue
	// 		}
	// 	}
	//
	// 	err = addPathToWatch(watcher, cwdFeatureFileFolders[k])
	// 	if err != nil {
	// 		return err
	// 	}
	// }

	<-done
	return nil
}

func addPathToWatch(w *rfsnotify.RWatcher, path string) error {
	err := w.AddRecursive(path)
	if err != nil {
		switch casted := err.(type) {
		case *fs.PathError:
			switch casted.Err {
			case unix.ENOENT:
				// folder not found, ignore it
				debug.IgnoreError(err)
			default:
				logrus.Error(err)
				return err
			}
		default:
			logrus.Error(err)
			return err
		}
	}

	logrus.Debugf("adding monitoring of folder '%s' for Feature file changes", path)
	return nil
}

// onFeatureFileEvent reacts to filesystem change event
func onFeatureFileEvent(w *rfsnotify.RWatcher, e fsnotify.Event) {
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
			_ = w.AddRecursive(e.Name)
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
	for _, v := range cwdFeatureFileFolders {
		if strings.HasPrefix(name, v) {
			reduced := strings.TrimPrefix(name, v)
			if len(reduced) < len(last) {
				last = reduced
			}
		}
	}
	return last
}

// StartFeatureFileWatcher creates the Feature File cache and inits the watcher
func StartFeatureFileWatcher() {
	var xerr fail.Error
	featureFileController.cache, xerr = cache.NewSingleMemoryCache(featureFileKind)
	if xerr != nil {
		panic(xerr.Error())
	}

	// Starts go routine watching changes in Feature File folders
	go func() {
		err := watchFeatureFileFolders()
		if err != nil {
			logrus.Error(err)
		}
	}()
}
