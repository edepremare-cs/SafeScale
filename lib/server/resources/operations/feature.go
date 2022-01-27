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
	"context"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"sync"

	"github.com/CS-SI/SafeScale/lib/utils/data/observer"
	"github.com/CS-SI/SafeScale/lib/utils/debug/callstack"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"golang.org/x/sys/unix"
	"gopkg.in/fsnotify.v1"

	"github.com/CS-SI/SafeScale/lib/protocol"
	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/resources"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/featuretargettype"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hostproperty"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/installmethod"
	propertiesv1 "github.com/CS-SI/SafeScale/lib/server/resources/properties/v1"
	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
	"github.com/CS-SI/SafeScale/lib/utils/data"
	"github.com/CS-SI/SafeScale/lib/utils/data/cache"
	"github.com/CS-SI/SafeScale/lib/utils/data/serialize"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/callstack"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

const (
	featureFileKind = "featurefile"
)

var featureFileCache *cache.SingleMemoryCache

// FeatureParameter describes a Feature parameter as defined by Feature file content
type FeatureParameter struct {
	name             string // contains the name of the parameter
	description      string // contains the description of the parameter
	defaultValue     string // contains default value of the parameter
	valueControlCode string
	hasDefault       bool // true if the parameter has a default value
	hasValueControl  bool
}

// NewFeatureParameter initiales an new instance of FeatureParameter
func NewFeatureParameter(name, description string, hasDefault bool, defaultValue string, hasValueControl bool, valueControlCode string) (FeatureParameter, fail.Error) {
	if name == "" {
		return FeatureParameter{}, fail.InvalidParameterCannotBeEmptyStringError("name")
	}

	out := FeatureParameter{
		name:             name,
		description:      description,
		hasDefault:       hasDefault,
		defaultValue:     defaultValue,
		hasValueControl:  hasValueControl,
		valueControlCode: valueControlCode,
	}
	return out, nil
}

// Name returns the name of the parameter
func (fp FeatureParameter) Name() string {
	return fp.name
}

// Description returns the description of the parameter
func (fp FeatureParameter) Description() string {
	return fp.description
}

// HasDefaultValue tells if the parameter has a default value
func (fp FeatureParameter) HasDefaultValue() bool {
	return fp.hasDefault
}

// DefaultValue returns the default value of the parameter
func (fp FeatureParameter) DefaultValue() (string, bool) {
	if fp.hasDefault {
		return fp.defaultValue, true
	}

	return "", false
}

// HasValueControl tells if the parameter has a default value
func (fp FeatureParameter) HasValueControl() bool {
	return fp.hasValueControl
}

// ValueControlCode returns the bash code to control the value
func (fp FeatureParameter) ValueControlCode() (string, bool) {
	if fp.hasValueControl {
		return fp.valueControlCode, true
	}

	return "", false
}

// ConditionedFeatureParameter describes a Feature prepared for use on a Target
type ConditionedFeatureParameter struct {
	FeatureParameter
	currentValue string // contains overloaded value of the parameter (ie provided by CLI)
}

// NewConditionedFeatureParameter creates an instance of ConditionedFeatureParameter from FeatureParameter and sets the current value
func NewConditionedFeatureParameter(parameter FeatureParameter, value *string) (ConditionedFeatureParameter, fail.Error) {
	out := ConditionedFeatureParameter{
		FeatureParameter: parameter,
	}
	if value == nil {
		if !parameter.HasDefaultValue() {
			return ConditionedFeatureParameter{}, fail.InvalidRequestError("missing value for parameter '%s'", parameter.name)
		}

		out.currentValue, _ = parameter.DefaultValue()
	} else {
		out.currentValue = *value
	}
	return out, nil
}

// Value returns the current value of the parameter
func (cfp ConditionedFeatureParameter) Value() string {
	return cfp.currentValue
}

type ConditionedFeatureParameters map[string]ConditionedFeatureParameter

// ToMap converts a ConditionedFeatureParameters to a data.Map (to be used in template)
func (cfp ConditionedFeatureParameters) ToMap() data.Map {
	out := data.Map{}
	for k, v := range cfp {
		out[k] = v.Value()
	}
	return out
}

// FeatureFile contains the information about an installable Feature
type FeatureFile struct {
	displayName     string                       // is the name of the feature
	fileName        string                       // is the name of the specification file
	displayFileName string                       // is the 'pretty' name of the specification file
	embedded        bool                         // tells if the Feature is embedded in SafeScale
	specs           *viper.Viper                 // is the Viper instance containing Feature file content
	parameters      map[string]FeatureParameter  // contains all the parameters defined in Feature file
	versionControl  map[string]string            // contains the templated bash code to determine version of a component used in the FeatureFile
	observersLock   *sync.RWMutex                // lock to access field 'observers'
	observers       map[string]observer.Observer // contains the Observers of the FeatureFile
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
	ce, xerr := featureFileCache.Get(name, cacheOptions...)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			debug.IgnoreError(xerr)
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
		newInstance, xerr = findEmbeddedFeature(name)
		if xerr != nil {
			return nil, xerr
		}

		logrus.Tracef("Loaded feature '%s' (%s)", newInstance.DisplayFilename(), newInstance.Filename())
	} else {
		v := viper.New()
		v.AddConfigPath(".")
		v.AddConfigPath("./features")
		v.AddConfigPath("./.safescale/features")
		v.AddConfigPath("$HOME/.safescale/features")
		v.AddConfigPath("$HOME/.config/safescale/features")
		v.AddConfigPath("/etc/safescale/features")
		v.SetConfigName(name)

		err := v.ReadInConfig()
		err = debug.InjectPlannedError(err)
		if err != nil {
			switch err.(type) {
			case viper.ConfigFileNotFoundError:
				newInstance, xerr = findEmbeddedFeature(name)
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

		logrus.Tracef("Loaded feature '%s' (%s)", newInstance.DisplayFilename(), newInstance.Filename())

		// if we can log the sha256 of the feature, do it
		filename := v.ConfigFileUsed()
		if filename != "" {
			content, err := ioutil.ReadFile(filename)
			if err != nil {
				return nil, fail.ConvertError(err)
			}

			logrus.Tracef("Loaded feature '%s' SHA256:%s", name, getSHA256Hash(string(content)))
		}
	}

	xerr = newInstance.Parse()
	if xerr != nil {
		return nil, xerr
	}

	return newInstance, nil
}

// findEmbeddedFeature returns the instance of the embedded feature called 'name', if it exists
func findEmbeddedFeature(name string) (*FeatureFile, fail.Error) {
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

	xerr := ff.parseParameters()
	if xerr != nil {
		return xerr
	}

	xerr = ff.parseInstallers()
	if xerr != nil {
		return xerr
	}

	return nil
}

// parseParameters parses the parameters and populates 'FeatureFile.parameters'
func (ff *FeatureFile) parseParameters() fail.Error {
	if ff.specs.IsSet("feature.parameters") {
		params, ok := ff.specs.Get("feature.parameters").([]interface{})
		if !ok {
			return fail.SyntaxError("unsupported content for keyword 'feature.parameters': must be a list of string or struct")
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
				name, ok := casted["name"]
				if !ok {
					return fail.SyntaxError("missing 'name' field in entry #%d of keyword 'feature.parameters'", k+1)
				}
				if strings.ContainsAny(name, "=:") {
					return fail.SyntaxError("field 'name' cannot contain '=' or ':' in entry #%d", k+1)
				}

				description, _ := casted["description"] // nolint
				value, hasDefaultValue := casted["value"]
				valueControlCode, hasValueControl := casted["control"]
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
	// Does nothing yet, need to review step handling to differentiate a step definition and a step realization
	return nil
}

// Feature contains the information about a FeatureFile to be installed
type Feature struct {
	file       *FeatureFile
	installers map[installmethod.Enum]Installer // defines the installers available for the Feature
	svc        iaas.Service                     // is the iaas.Service to use to interact with Cloud Provider
}

// Not used
// // FeatureNullValue returns a *Feature corresponding to a null value
// func FeatureNullValue() *Feature {
// 	return &Feature{}
// }

// ListFeatures lists all features suitable for hosts or clusters
func ListFeatures(svc iaas.Service, suitableFor string) (_ []interface{}, xerr fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}

	var (
		cfgFiles []interface{}
		paths    []string
	)

	// use all embedded features as base of list of features
	list := make(map[string]*FeatureFile, len(allEmbeddedFeaturesMap))
	for k, v := range allEmbeddedFeaturesMap {
		list[k] = v
	}

	paths = append(paths, utils.AbsPathify("$HOME/.safescale/features"))
	paths = append(paths, utils.AbsPathify("$HOME/.config/safescale/features"))
	paths = append(paths, utils.AbsPathify("/etc/safescale/features"))

	for _, path := range paths {
		files, err := ioutil.ReadDir(path)
		if err != nil {
			debug.IgnoreError(err)
			continue
		}
		for _, f := range files {
			if strings.HasSuffix(strings.ToLower(f.Name()), ".yml") {
				featFile, xerr := LoadFeatureFile(svc, strings.Replace(strings.ToLower(f.Name()), ".yml", "", 1), false)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					logrus.Warn(xerr) // Don't hide errors
					continue
				}

				list[featFile.GetName()] = featFile
			}
		}
	}

	for _, item := range list {
		switch suitableFor {
		case "host":
			yamlKey := "feature.suitableFor.host"
			if item.Specs().IsSet(yamlKey) {
				value := strings.ToLower(item.Specs().GetString(yamlKey))
				if value == "ok" || value == "yes" || value == "true" || value == "1" {
					cfgFiles = append(cfgFiles, item.fileName)
				}
			}
		case "cluster":
			yamlKey := "feature.suitableFor.cluster"
			if item.Specs().IsSet(yamlKey) {
				values := strings.Split(strings.ToLower(item.Specs().GetString(yamlKey)), ",")
				if values[0] == "all" || values[0] == "k8s" || values[0] == "boh" {
					cfg := struct {
						FeatureName    string   `json:"feature"`
						ClusterFlavors []string `json:"available-cluster-flavors"`
					}{item.displayName, []string{}}

					cfg.ClusterFlavors = append(cfg.ClusterFlavors, values...)

					cfgFiles = append(cfgFiles, cfg)
				}
			}
		default:
			return nil, fail.SyntaxError("unknown parameter value: %s (should be 'host' or 'cluster')", suitableFor)
		}
	}

	return cfgFiles, nil
}

// NewFeature searches for a spec file name 'name' and initializes a new Feature object
// with its content
// error contains :
//    - fail.ErrNotFound if no Feature is found by its name
//    - fail.ErrSyntax if Feature found contains syntax error
func NewFeature(svc iaas.Service, name string) (_ resources.Feature, ferr fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}
	if name == "" {
		return nil, fail.InvalidParameterError("name", "cannot be empty string")
	}

	featureFileInstance, xerr := LoadFeatureFile(svc, name, false)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	featureInstance := &Feature{
		file: featureFileInstance,
		svc:  svc,
	}
	return featureInstance, nil
}

// NewEmbeddedFeature searches for an embedded featured named 'name' and initializes a new Feature object
// with its content
func NewEmbeddedFeature(svc iaas.Service, name string) (_ resources.Feature, xerr fail.Error) {
	if svc == nil {
		return nil, fail.InvalidParameterCannotBeNilError("svc")
	}
	if name == "" {
		return nil, fail.InvalidParameterError("name", "cannot be empty string")
	}

	featureFileInstance, xerr := LoadFeatureFile(svc, name, true)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	featureInstance := &Feature{
		file: featureFileInstance,
		svc:  svc,
	}

	return featureInstance, nil
}

// IsNull tells if the instance represents a null value
func (f *Feature) IsNull() bool {
	return f == nil || f.file == nil
}

// Clone ...
// satisfies interface data.Clonable
func (instance *Feature) Clone() data.Clonable {
	res := &Feature{}
	return res.Replace(instance)
}

// Replace ...
// satisfies interface data.Clonable
// may panic
func (instance *Feature) Replace(p data.Clonable) data.Clonable {
	// Do not test with IsNull(), it's allowed to clone a null value...
	if instance == nil || p == nil {
		return instance
	}

	// FIXME: Replace should also return an error
	src, _ := p.(*Feature) // nolint
	// VPL: Not used yet, need to think if we should return an error or panic, or something else
	// src, ok := p.(*Feature)
	// if !ok {
	// 	panic("failed to cast p to '*Feature'")
	// }
	*instance = *src
	instance.installers = make(map[installmethod.Enum]Installer, len(src.installers))
	for k, v := range src.installers {
		instance.installers[k] = v
	}
	return instance
}

// GetName returns the display name of the Feature, with error handling
func (instance *Feature) GetName() string {
	if instance.IsNull() {
		return ""
	}

	return f.file.displayName
}

// GetID ...
func (instance *Feature) GetID() string {
	if instance.IsNull() {
		return ""
	}
	return instance.GetName()
}

// GetFilename returns the filename of the Feature definition, with error handling
func (instance *Feature) GetFilename() string {
	if instance.IsNull() {
		return ""
	}

	return f.file.fileName
}

// GetDisplayFilename returns the filename of the Feature definition, beautifulled, with error handling
func (instance *Feature) GetDisplayFilename() string {
	if instance.IsNull() {
		return ""
	}
	return f.file.displayFileName
}

// installerOfMethod instanciates the right installer corresponding to the method
func (instance *Feature) installerOfMethod(m installmethod.Enum) Installer {
	if instance.IsNull() {
		return nil
	}
	var installer Installer
	switch m {
	case installmethod.Bash:
		installer = newBashInstaller()
	case installmethod.Apt:
		installer = NewAptInstaller()
	case installmethod.Yum:
		installer = NewYumInstaller()
	case installmethod.Dnf:
		installer = NewDnfInstaller()
	case installmethod.None:
		installer = newNoneInstaller()
	}
	return installer
}

// Specs returns a copy of the spec file (we don't want external use to modify Feature.specs)
func (instance *Feature) Specs() *viper.Viper {
	if instance.IsNull() {
		return &viper.Viper{}
	}

	return f.file.Specs()
}

// Applicable tells if the Feature is installable on the target
func (instance *Feature) Applicable(t resources.Targetable) bool {
	if instance.IsNull() {
		return false
	}

	methods := t.InstallMethods()
	for _, k := range methods {
		installer := instance.installerOfMethod(k)
		if installer != nil {
			return true
		}
	}
	return false
}

// Check if Feature is installed on target
// Check is ok if error is nil and Results.Successful() is true
func (instance *Feature) Check(ctx context.Context, target resources.Targetable, v data.Map, s resources.FeatureSettings) (_ resources.Results, xerr fail.Error) {
	if instance.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if ctx == nil {
		return nil, fail.InvalidParameterCannotBeNilError("ctx")
	}
	if target == nil {
		return nil, fail.InvalidParameterCannotBeNilError("target")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return nil, xerr
			}
		default:
			return nil, xerr
		}
	}

	featureName := instance.GetName()
	targetName := target.GetName()
	targetType := strings.ToLower(target.TargetType().String())
	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.feature"), "(): '%s' on %s '%s'", featureName, targetType, targetName).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	// -- passive check if feature is installed on target
	switch target.(type) { // nolint
	case resources.Host:
		var found bool
		castedTarget, ok := target.(*Host)
		if !ok {
			return &results{}, fail.InconsistentError("failed to cast target to '*Host'")
		}

		xerr = castedTarget.Review(func(clonable data.Clonable, props *serialize.JSONProperties) fail.Error {
			return props.Inspect(hostproperty.FeaturesV1, func(clonable data.Clonable) fail.Error {
				hostFeaturesV1, ok := clonable.(*propertiesv1.HostFeatures)
				if !ok {
					return fail.InconsistentError("'*propertiesv1.HostFeatures' expected, '%s' provided", reflect.TypeOf(clonable).String())
				}
				_, found = hostFeaturesV1.Installed[instance.GetName()]
				return nil
			})
		})
		if xerr != nil {
			return nil, xerr
		}
		if found {
			outcomes := &results{}
			_ = outcomes.Add(featureName, &unitResults{
				targetName: &stepResult{
					completed: true,
					success:   true,
				},
			})
			return outcomes, nil
		}
	}

	// -- fall back to active check
	installer, xerr := instance.findInstallerForTarget(target, "check")
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	logrus.Debugf("Checking if Feature '%s' is installed on %s '%s'...\n", featureName, targetType, targetName)

	// Inits and checks target parameters
	conditionedParameters, xerr := f.conditionParameters(v)
	if xerr != nil {
		return nil, xerr
	}

	myV := conditionedParameters.ToMap()
	xerr = target.ComplementFeatureParameters(ctx, myV)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// // Checks required parameters have their values
	// xerr = checkRequiredParameters(*instance, myV)
	// xerr = debug.InjectPlannedFail(xerr)
	// if xerr != nil {
	// 	return nil, xerr
	// }
	//
	r, xerr := installer.Check(ctx, instance, target, myV, s)
	if xerr != nil {
		return nil, xerr
	}

	// FIXME: restore Feature check using iaas.ResourceCache
	// _ = checkCache.ForceSet(cacheKey, results)
	return r, xerr
}

// conditionParameters builds a map of ConditionedFeatureParameter with final value set, picking it from externals if provided (otherwise, default value is set)
// Returned error may be:
//  - nil: everything went well
//  - fail.InvalidRequestError: a required parameter is missing (value not provided in externals and no default value defined)
func (f Feature) conditionParameters(externals data.Map) (ConditionedFeatureParameters, fail.Error) {
	var xerr fail.Error
	conditioned := make(ConditionedFeatureParameters)
	for k, v := range f.file.parameters {
		value, ok := externals[k].(string)
		if ok {
			conditioned[k], xerr = NewConditionedFeatureParameter(v, &value)
		} else {
			value, ok := externals[f.GetName()+":"+k].(string)
			if ok {
				conditioned[k], xerr = NewConditionedFeatureParameter(v, &value)
			} else {
				conditioned[k], xerr = NewConditionedFeatureParameter(v, nil)
			}
		}
		if xerr != nil {
			return nil, xerr
		}
	}
	return conditioned, nil
}

// findInstallerForTarget isolates the available installer to use for target (one that is define in the file and applicable on target)
func (instance *Feature) findInstallerForTarget(target resources.Targetable, action string) (installer Installer, xerr fail.Error) {
	methods := target.InstallMethods()
	w := f.file.specs.GetStringMap("feature.install")
	for i := uint8(1); i <= uint8(len(methods)); i++ {
		meth := methods[i]
		if _, ok := w[strings.ToLower(meth.String())]; ok {
			if installer = instance.installerOfMethod(meth); installer != nil {
				break
			}
		}
	}
	if installer == nil {
		return nil, fail.NotAvailableError("failed to find a way to %s '%s'", action, instance.GetName())
	}
	return installer, nil
}

// // checkRequiredParameters Check if required parameters defined in specification file have been set in 'v'
// func checkRequiredParameters(f Feature, v data.Map) fail.Error {
// 	if f.file.specs.IsSet("feature.parameters") {
// 		// params := f.file.specs.GetStringSlice("feature.parameters")
// 		params, ok := f.file.specs.Get("feature.parameters").([]interface{})
// 		if !ok {
// 			return fail.SyntaxError("unsupported content for keyword 'feature.parameters': must be a list of string or struct")
// 		}
// 		for k, p := range params {
// 			switch p := p.(type) {
// 			case string:
// 				if p == "" {
// 					continue
// 				}
// 				splitted := strings.Split(p, "=")
// 				if _, ok := v[splitted[0]]; !ok {
// 					if len(splitted) == 1 {
// 						return fail.InvalidRequestError("missing value for parameter '%s'", p)
// 					}
//
// 					v[splitted[0]] = splitted[1]
// 				}
//
// 			case map[interface{}]interface{}:
// 				casted := data.ToMapStringOfString(p)
// 				name, ok := casted["name"]
// 				if !ok {
// 					return fail.SyntaxError("missing 'name' field in entry #%d of keyword 'feature.parameters'", k+1)
// 				}
// 				if _, ok := v[name]; !ok {
// 					value, ok := casted["value"]
// 					if !ok {
// 						return fail.InvalidRequestError("missing value for parameter '%s'", name)
// 					}
//
// 					v[name] = value
// 				}
//
// 			default:
// 				return fail.SyntaxError("unsupported content for keyword 'feature.parameters': must be a list of string or struct")
// 			}
// 		}
// 	}
// 	return nil
// }

// Add installs the Feature on the target
// Installs succeeds if error == nil and Results.Successful() is true
func (instance *Feature) Add(ctx context.Context, target resources.Targetable, v data.Map, s resources.FeatureSettings) (_ resources.Results, ferr fail.Error) {
	defer fail.OnPanic(&ferr)

	if instance.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if ctx == nil {
		return nil, fail.InvalidParameterCannotBeNilError("ctx")
	}
	if target == nil {
		return nil, fail.InvalidParameterCannotBeNilError("target")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return nil, xerr
			}
		default:
			return nil, xerr
		}
	}

	featureName := instance.GetName()
	targetName := target.GetName()
	targetType := target.TargetType().String()

	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.feature"), "(): '%s' on %s '%s'", featureName, targetType, targetName).WithStopwatch().Entering()
	defer tracer.Exiting()

	defer temporal.NewStopwatch().OnExitLogInfo(
		fmt.Sprintf("Starting addition of Feature '%s' on %s '%s'...", featureName, targetType, targetName),
		fmt.Sprintf("Ending addition of Feature '%s' on %s '%s'", featureName, targetType, targetName),
	)()

	installer, xerr := instance.findInstallerForTarget(target, "check")
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// Inits and checks target parameters
	conditionedParameters, xerr := f.conditionParameters(v)
	if xerr != nil {
		return nil, xerr
	}

	myV := conditionedParameters.ToMap()

	// Inits target parameters
	xerr = target.ComplementFeatureParameters(ctx, myV)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// // Checks required parameters have value
	// xerr = checkRequiredParameters(*instance, myV)
	// xerr = debug.InjectPlannedFail(xerr)
	// if xerr != nil {
	// 	return nil, xerr
	// }

	if !s.AddUnconditionally {
		results, xerr := instance.Check(ctx, target, v, s)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, fail.Wrap(xerr, "failed to check Feature '%s'", featureName)
		}

		if results.Successful() {
			logrus.Infof("Feature '%s' is already installed.", featureName)
			return results, nil
		}
	}

	if !s.SkipFeatureRequirements {
		xerr = instance.installRequirements(ctx, target, v, s)
		xerr = debug.InjectPlannedFail(xerr)
		if xerr != nil {
			return nil, fail.Wrap(xerr, "failed to install requirements")
		}
	}

	results, xerr := installer.Add(ctx, instance, target, myV, s)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	xerr = registerOnSuccessfulHostsInCluster(instance.svc, target, instance, nil, results)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// FIXME: restore Feature check cache using iaas.ResourceCache
	// _ = checkCache.ForceSet(featureName()+"@"+targetName, results)

	return results, target.RegisterFeature(instance, nil, target.TargetType() == featuretargettype.Cluster)
}

// Remove uninstalls the Feature from the target
func (instance *Feature) Remove(ctx context.Context, target resources.Targetable, v data.Map, s resources.FeatureSettings) (_ resources.Results, xerr fail.Error) {
	if instance.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if ctx == nil {
		return nil, fail.InvalidParameterCannotBeNilError("ctx")
	}
	if target == nil {
		return nil, fail.InvalidParameterCannotBeNilError("target")
	}

	task, xerr := concurrency.TaskFromContext(ctx)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotAvailable:
			task, xerr = concurrency.VoidTask()
			if xerr != nil {
				return nil, xerr
			}
		default:
			return nil, xerr
		}
	}

	featureName := instance.GetName()
	targetName := target.GetName()
	targetType := target.TargetType().String()
	tracer := debug.NewTracer(task, tracing.ShouldTrace("resources.feature"), "(): '%s' on %s '%s'", featureName, targetType, targetName).WithStopwatch().Entering()
	defer tracer.Exiting()
	defer fail.OnExitLogError(&xerr, tracer.TraceMessage(""))

	var (
		results resources.Results
		// installer Installer
	)

	installer, xerr := instance.findInstallerForTarget(target, "check")
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	defer temporal.NewStopwatch().OnExitLogInfo(
		fmt.Sprintf("Starting removal of Feature '%s' from %s '%s'", featureName, targetType, targetName),
		fmt.Sprintf("Ending removal of Feature '%s' from %s '%s'", featureName, targetType, targetName),
	)()

	// Inits and checks target parameters
	conditionedParameters, xerr := f.conditionParameters(v)
	if xerr != nil {
		return nil, xerr
	}

	myV := conditionedParameters.ToMap()

	// Inits target parameters
	xerr = target.ComplementFeatureParameters(ctx, myV)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	// // Checks required parameters have value
	// xerr = checkRequiredParameters(*instance, myV)
	// xerr = debug.InjectPlannedFail(xerr)
	// if xerr != nil {
	// 	return nil, xerr
	// }

	results, xerr = installer.Remove(ctx, instance, target, myV, s)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return results, xerr
	}

	xerr = unregisterOnSuccessfulHostsInCluster(instance.svc, target, instance, results)
	xerr = debug.InjectPlannedFail(xerr)
	if xerr != nil {
		return nil, xerr
	}

	return results, target.UnregisterFeature(instance.GetName())
}

const yamlKey = "feature.requirements.features"

// GetRequirements returns a list of features needed as requirements
func (instance *Feature) GetRequirements() (map[string]struct{}, fail.Error) {
	emptyMap := map[string]struct{}{}
	if instance.IsNull() {
		return emptyMap, fail.InvalidInstanceError()
	}

	out := make(map[string]struct{}, len(f.file.specs.GetStringSlice(yamlKey)))
	for _, r := range f.file.specs.GetStringSlice(yamlKey) {
		out[r] = struct{}{}
	}
	return out, nil
}

// installRequirements walks through requirements and installs them if needed
func (f *Feature) installRequirements(ctx context.Context, t resources.Targetable, v data.Map, s resources.FeatureSettings) fail.Error {
	if f.file.specs.IsSet(yamlKey) {
		{
			msgHead := fmt.Sprintf("Checking requirements of Feature '%s'", instance.GetName())
			var msgTail string
			switch t.TargetType() {
			case featuretargettype.Host:
				msgTail = fmt.Sprintf("on host '%s'", t.(data.Identifiable).GetName())
			case featuretargettype.Node:
				msgTail = fmt.Sprintf("on cluster node '%s'", t.(data.Identifiable).GetName())
			case featuretargettype.Cluster:
				msgTail = fmt.Sprintf("on cluster '%s'", t.(data.Identifiable).GetName())
			}
			logrus.Debugf("%s %s...", msgHead, msgTail)
		}

		targetIsCluster := t.TargetType() == featuretargettype.Cluster

		// clone FeatureSettings to set DoNotUpdateHostMetadataInClusterContext
		for _, requirement := range f.file.specs.GetStringSlice(yamlKey) {
			needed, xerr := NewFeature(f.svc, requirement)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return fail.Wrap(xerr, "failed to find required Feature '%s'", requirement)
			}

			results, xerr := needed.Check(ctx, t, v, s)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return fail.Wrap(xerr, "failed to check required Feature '%s' for Feature '%s'", requirement, instance.GetName())
			}

			if !results.Successful() {
				results, xerr := needed.Add(ctx, t, v, s)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return fail.Wrap(xerr, "failed to install required Feature '%s'", requirement)
				}

				if !results.Successful() {
					return fail.NewError("failed to install required Feature '%s':\n%s", requirement, results.AllErrorMessages())
				}

				// Register the needed Feature as a requirement for instance
				xerr = t.RegisterFeature(needed, instance, targetIsCluster)
				xerr = debug.InjectPlannedFail(xerr)
				if xerr != nil {
					return xerr
				}
			}
		}

	}
	return nil
}

func registerOnSuccessfulHostsInCluster(svc iaas.Service, target resources.Targetable, installed resources.Feature, requiredBy resources.Feature, results resources.Results) fail.Error {
	if target.TargetType() == featuretargettype.Cluster {
		// Walk through results and register Feature in successful hosts
		successfulHosts := map[string]struct{}{}
		for _, k := range results.Keys() {
			r := results.ResultsOfKey(k)
			for _, l := range r.Keys() {
				s := r.ResultOfKey(l)
				if s != nil {
					if s.Successful() {
						successfulHosts[l] = struct{}{}
					}
				}
			}
		}
		for k := range successfulHosts {
			host, xerr := LoadHost(svc, k)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}

			xerr = host.RegisterFeature(installed, requiredBy, true)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}
		}
	}
	return nil
}

func unregisterOnSuccessfulHostsInCluster(svc iaas.Service, target resources.Targetable, installed resources.Feature, results resources.Results) fail.Error {
	if target.TargetType() == featuretargettype.Cluster {
		// Walk through results and register Feature in successful hosts
		successfulHosts := map[string]struct{}{}
		for _, k := range results.Keys() {
			r := results.ResultsOfKey(k)
			for _, l := range r.Keys() {
				s := r.ResultOfKey(l)
				if s != nil {
					if s.Successful() {
						successfulHosts[l] = struct{}{}
					}
				}
			}
		}
		for k := range successfulHosts {
			host, xerr := LoadHost(svc, k)
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}

			xerr = host.UnregisterFeature(installed.GetName())
			xerr = debug.InjectPlannedFail(xerr)
			if xerr != nil {
				return xerr
			}
		}
	}
	return nil
}

// ToProtocol converts a Feature to *protocol.FeatureResponse
func (instance Feature) ToProtocol() *protocol.FeatureResponse {
	out := &protocol.FeatureResponse{
		Name:     instance.GetName(),
		FileName: instance.GetDisplayFilename(),
	}
	return out
}

func (instance Feature) ListParametersWithControl() []string {
	return nil
}

func (instance Feature) VersionForParameter(p string) (string, fail.Error) {
	return "", fail.NotImplementedError()
}

// ExtractFeatureParameters convert a slice of string in format a=b into a map index on 'a' with value 'b'
func ExtractFeatureParameters(params []string) data.Map {
	out := data.Map{}
	for _, v := range params {
		splitted := strings.Split(v, "=")
		if len(splitted) > 1 {
			out[splitted[0]] = splitted[1]
		} else {
			out[splitted[0]] = ""
		}
	}
	return out
}

func init() {
	var xerr fail.Error
	featureFileCache, xerr = cache.NewSingleMemoryCache(featureFileKind)
	if xerr != nil {
		panic(xerr.Error())
	}
}
