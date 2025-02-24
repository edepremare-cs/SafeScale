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

package iaas

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/CS-SI/SafeScale/lib/utils/temporal"
	scribble "github.com/nanobox-io/golang-scribble"
	uuid "github.com/satori/go.uuid"
	"github.com/sirupsen/logrus"
	"github.com/xrash/smetrics"

	"github.com/CS-SI/SafeScale/lib/server/iaas/objectstorage"
	"github.com/CS-SI/SafeScale/lib/server/iaas/providers"
	"github.com/CS-SI/SafeScale/lib/server/iaas/userdata"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	imagefilters "github.com/CS-SI/SafeScale/lib/server/resources/abstract/filters/images"
	templatefilters "github.com/CS-SI/SafeScale/lib/server/resources/abstract/filters/templates"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hoststate"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/volumestate"
	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/CS-SI/SafeScale/lib/utils/crypt"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/strprocess"
)

//go:generate minimock -o mocks/mock_serviceapi.go -i github.com/CS-SI/SafeScale/lib/server/iaas.Service

// Service consolidates Provider and ObjectStorage.Location interfaces in a single interface
// completed with higher-level methods
type Service interface {
	CreateHostWithKeyPair(abstract.HostRequest) (*abstract.HostFull, *userdata.Content, *abstract.KeyPair, fail.Error)
	FilterImages(string) ([]abstract.Image, fail.Error)
	FindTemplateBySizing(abstract.HostSizingRequirements) (*abstract.HostTemplate, fail.Error)
	FindTemplateByName(string) (*abstract.HostTemplate, fail.Error)
	GetProviderName() (string, fail.Error)
	GetMetadataBucket() (abstract.ObjectStorageBucket, fail.Error)
	GetMetadataKey() (*crypt.Key, fail.Error)
	InspectHostByName(string) (*abstract.HostFull, fail.Error)
	InspectSecurityGroupByName(networkID string, name string) (*abstract.SecurityGroup, fail.Error)
	ListHostsByName(bool) (map[string]*abstract.HostFull, fail.Error)
	ListTemplatesBySizing(abstract.HostSizingRequirements, bool) ([]*abstract.HostTemplate, fail.Error)
	ObjectStorageConfiguration() objectstorage.Config
	SearchImage(string) (*abstract.Image, fail.Error)
	TenantCleanup(bool) fail.Error // cleans up the data relative to SafeScale from tenant (not implemented yet)
	WaitHostState(string, hoststate.Enum, time.Duration) fail.Error
	WaitVolumeState(string, volumestate.Enum, time.Duration) (*abstract.Volume, fail.Error)

	GetCache(string) (*ResourceCache, fail.Error)

	// Provider --- from interface iaas.Providers ---
	providers.Provider

	LookupRuleInSecurityGroup(*abstract.SecurityGroup, *abstract.SecurityGroupRule) (bool, fail.Error)

	// Location --- from interface objectstorage.Location ---
	objectstorage.Location
}

// service is the implementation struct of interface Service
type service struct {
	providers.Provider
	objectstorage.Location

	tenantName string

	//	metadataBucket objectstorage.GetBucket
	metadataBucket abstract.ObjectStorageBucket
	metadataKey    *crypt.Key

	whitelistTemplateREs []*regexp.Regexp
	blacklistTemplateREs []*regexp.Regexp
	whitelistImageREs    []*regexp.Regexp
	blacklistImageREs    []*regexp.Regexp

	cache     serviceCache
	cacheLock *sync.Mutex
}

const (
	// CoreDRFWeight is the Dominant Resource Fairness weight of a core
	CoreDRFWeight float32 = 1.0
	// RAMDRFWeight is the Dominant Resource Fairness weight of 1 GB of RAM
	RAMDRFWeight float32 = 1.0 / 8.0
	// DiskDRFWeight is the Dominant Resource Fairness weight of 1 GB of Disk
	DiskDRFWeight float32 = 1.0 / 16.0
)

// RankDRF computes the Dominant Resource Fairness Rank of a host template
func RankDRF(t *abstract.HostTemplate) float32 {
	fc := float32(t.Cores)
	fr := t.RAMSize
	fd := float32(t.DiskSize)
	return fc*CoreDRFWeight + fr*RAMDRFWeight + fd*DiskDRFWeight
}

// ByRankDRF implements sort.Interface for []HostTemplate based on
// the Dominant Resource Fairness
type ByRankDRF []*abstract.HostTemplate

func (a ByRankDRF) Len() int           { return len(a) }
func (a ByRankDRF) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a ByRankDRF) Less(i, j int) bool { return RankDRF(a[i]) < RankDRF(a[j]) }

// NullService creates a service instance corresponding to null value
func NullService() *service { // nolint
	return &service{}
}

// IsNull tells if the instance is null value
func (svc *service) IsNull() bool {
	return svc == nil || svc.Provider == nil
}

// GetProviderName ...
func (svc service) GetProviderName() (string, fail.Error) {
	if svc.IsNull() {
		return "", nil
	}
	svcName, xerr := svc.GetName()
	if xerr != nil {
		return "", xerr
	}
	return svcName, nil
}

// GetName ...
// Satisfies interface data.Identifiable
func (svc service) GetName() (string, fail.Error) {
	if svc.IsNull() {
		return "", nil
	}

	return svc.tenantName, nil
}

// GetCache returns the data.Cache instance corresponding to the name passed as parameter
// If the cache does not exist, create it
func (svc *service) GetCache(name string) (_ *ResourceCache, xerr fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if name == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("name")
	}

	svc.cacheLock.Lock()
	defer svc.cacheLock.Unlock()

	if _, ok := svc.cache.resources[name]; !ok {
		rc, xerr := NewResourceCache(name)
		if xerr != nil {
			return rc, xerr
		}

		svc.cache.resources[name] = rc
	}
	return svc.cache.resources[name], nil
}

// GetMetadataBucket returns the bucket instance describing metadata bucket
func (svc service) GetMetadataBucket() (abstract.ObjectStorageBucket, fail.Error) {
	if svc.IsNull() {
		return abstract.ObjectStorageBucket{}, nil
	}
	return svc.metadataBucket, nil
}

// GetMetadataKey returns the key used to crypt data in metadata bucket
func (svc service) GetMetadataKey() (*crypt.Key, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if svc.metadataKey == nil {
		return nil, fail.NotFoundError("no crypt key defined for metadata content")
	}
	return svc.metadataKey, nil
}

// ChangeProvider allows to change provider interface of service object (mainly for test purposes)
func (svc *service) ChangeProvider(provider providers.Provider) fail.Error {
	if svc.IsNull() {
		return fail.InvalidInstanceError()
	}
	if provider == nil {
		return fail.InvalidParameterCannotBeNilError("provider")
	}
	svc.Provider = provider
	return nil
}

// WaitHostState waits until a host achieves state 'state'
// If host is in error state, returns utils.ErrNotAvailable
// If timeout is reached, returns utils.ErrTimeout
func (svc service) WaitHostState(hostID string, state hoststate.Enum, timeout time.Duration) (rerr fail.Error) {
	if svc.IsNull() {
		return fail.InvalidInstanceError()
	}
	if hostID == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("hostID")
	}

	timer := time.After(timeout)
	host := abstract.NewHostFull()
	host.Core.ID = hostID

	errCh := make(chan fail.Error)
	done := make(chan struct{})
	defer close(errCh)

	go func() {
		for {
			select {
			case <-done: // only when it's closed
				return
			default:
			}

			host, rerr = svc.InspectHost(host) // FIXME: all service functions should accept ctx in order to be canceled
			if rerr != nil {
				errCh <- rerr
				return
			}
			if host.CurrentState == state {
				errCh <- nil
				return
			}
			if host.CurrentState == hoststate.Error {
				errCh <- fail.NotAvailableError("host in error state") // FIXME: This is NOT an error
				return
			}

			select {
			case <-done: // only when it's closed
				return
			default:
				time.Sleep(temporal.GetMinDelay())
			}
		}
	}()

	select {
	case <-timer:
		close(done)
		return fail.TimeoutError(nil, timeout, "Wait state timeout")
	case rErr := <-errCh:
		close(done)
		if rErr != nil {
			return rErr
		}
		return nil
	}
}

// WaitVolumeState waits a host achieve state
// If timeout is reached, returns utils.ErrTimeout
func (svc service) WaitVolumeState(
	volumeID string, state volumestate.Enum, timeout time.Duration,
) (*abstract.Volume, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if volumeID == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("volumeID")
	}

	cout := make(chan int)
	next := make(chan bool)
	vc := make(chan *abstract.Volume)

	go pollVolume(svc, volumeID, state, cout, next, vc)
	for {
		select {
		case res := <-cout:
			if res == 0 {
				return nil, fail.NewError("error getting host state")
			}
			if res == 1 {
				return <-vc, nil
			}
			if res == 2 {
				next <- true
			}
		case <-time.After(timeout):
			next <- false
			return nil, fail.TimeoutError(nil, timeout, "Wait host state timeout")
		}
	}
}

func pollVolume(
	svc service, volumeID string, state volumestate.Enum, cout chan int, next chan bool, hostc chan *abstract.Volume,
) {
	for {
		v, err := svc.InspectVolume(volumeID)
		if err != nil {
			cout <- 0
			return
		}
		if v.State == state {
			cout <- 1
			hostc <- v
			return
		}
		cout <- 2
		if !<-next {
			return
		}
	}
}

// ListTemplates lists available host templates, if all bool is true, all templates are returned, if not, templates are filtered using blacklists and whitelists
// Host templates are sorted using Dominant Resource Fairness Algorithm
func (svc service) ListTemplates(all bool) ([]abstract.HostTemplate, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	allTemplates, err := svc.Provider.ListTemplates(all)
	if err != nil {
		return nil, err
	}

	if all {
		return allTemplates, nil
	}

	return svc.reduceTemplates(allTemplates, svc.whitelistTemplateREs, svc.blacklistTemplateREs), nil
}

// FindTemplateByName returns the template by its name
func (svc service) FindTemplateByName(name string) (*abstract.HostTemplate, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	allTemplates, err := svc.Provider.ListTemplates(true)
	if err != nil {
		return nil, err
	}
	for _, i := range allTemplates {
		i := i
		if i.Name == name {
			return &i, nil
		}
	}
	return nil, fail.NotFoundError(fmt.Sprintf("template named '%s' not found", name))
}

// FindTemplateBySizing returns an abstracted template corresponding to the Host Sizing Requirements
func (svc service) FindTemplateBySizing(sizing abstract.HostSizingRequirements) (*abstract.HostTemplate, fail.Error) {
	useScannerDB := sizing.MinGPU > 0 || sizing.MinCPUFreq > 0
	templates, xerr := svc.ListTemplatesBySizing(sizing, useScannerDB)
	if xerr != nil {
		return nil, fail.Wrap(xerr, "failed to find template corresponding to requested resources")
	}

	var template *abstract.HostTemplate
	if len(templates) > 0 {
		template = templates[0]
		msg := fmt.Sprintf(
			"Selected host template: '%s' (%d core%s", template.Name, template.Cores,
			strprocess.Plural(uint(template.Cores)),
		)
		if template.CPUFreq > 0 {
			msg += fmt.Sprintf(" at %.01f GHz", template.CPUFreq)
		}
		msg += fmt.Sprintf(", %.01f GB RAM, %d GB disk", template.RAMSize, template.DiskSize)
		if template.GPUNumber > 0 {
			msg += fmt.Sprintf(", %d GPU%s", template.GPUNumber, strprocess.Plural(uint(template.GPUNumber)))
			if template.GPUType != "" {
				msg += fmt.Sprintf(" %s", template.GPUType)
			}
		}
		msg += ")"
		logrus.Infof(msg)
	} else {
		logrus.Errorf("failed to find template corresponding to requested resources")
		return nil, fail.Wrap(xerr, "failed to find template corresponding to requested resources")
	}
	return template, nil
}

// reduceTemplates filters from template slice the entries satisfying whitelist and blacklist regexps
func (svc service) reduceTemplates(
	tpls []abstract.HostTemplate, whitelistREs, blacklistREs []*regexp.Regexp,
) []abstract.HostTemplate {
	var finalFilter *templatefilters.Filter
	if len(whitelistREs) > 0 {
		// finalFilter = templatefilters.NewFilter(filterTemplatesByRegexSlice(svc.whitelistTemplateREs))
		finalFilter = templatefilters.NewFilter(filterTemplatesByRegexSlice(whitelistREs))
	}
	if len(blacklistREs) > 0 {
		//		blackFilter := templatefilters.NewFilter(filterTemplatesByRegexSlice(svc.blacklistTemplateREs))
		blackFilter := templatefilters.NewFilter(filterTemplatesByRegexSlice(blacklistREs))
		if finalFilter == nil {
			finalFilter = blackFilter.Not()
		} else {
			finalFilter = finalFilter.And(blackFilter.Not())
		}
	}
	if finalFilter != nil {
		return templatefilters.FilterTemplates(tpls, finalFilter)
	}
	return tpls
}

func filterTemplatesByRegexSlice(res []*regexp.Regexp) templatefilters.Predicate {
	return func(tpl abstract.HostTemplate) bool {
		for _, re := range res {
			if re.Match([]byte(tpl.Name)) {
				return true
			}
		}
		return false
	}
}

// ListTemplatesBySizing select templates satisfying sizing requirements
// returned list is ordered by size fitting
func (svc service) ListTemplatesBySizing(
	sizing abstract.HostSizingRequirements, force bool,
) (selectedTpls []*abstract.HostTemplate, rerr fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	tracer := debug.NewTracer(nil, true, "").Entering()
	defer tracer.Exiting()

	allTpls, rerr := svc.ListTemplates(false)
	if rerr != nil {
		return nil, rerr
	}

	scannerTpls := map[string]bool{}
	askedForSpecificScannerInfo := sizing.MinGPU >= 0 || sizing.MinCPUFreq != 0
	if askedForSpecificScannerInfo {
		_ = os.MkdirAll(utils.AbsPathify("$HOME/.safescale/scanner"), 0777)
		db, err := scribble.New(utils.AbsPathify("$HOME/.safescale/scanner/db"), nil)
		if err != nil {
			if force {
				logrus.Warnf(
					"Problem creating / accessing Scanner database, ignoring GPU and Freq parameters for now...: %v",
					err,
				)
			} else {
				var noHostError string
				if sizing.MinCPUFreq <= 0 {
					noHostError = fmt.Sprintf(
						"unable to create a host with '%d' GPUs, problem accessing Scanner database: %v", sizing.MinGPU,
						err,
					)
				} else {
					noHostError = fmt.Sprintf(
						"unable to create a host with '%d' GPUs and '%.01f' MHz clock frequency, problem accessing Scanner database: %v",
						sizing.MinGPU, sizing.MinCPUFreq, err,
					)
				}
				return nil, fail.NewError(noHostError)
			}
		} else {
			authOpts, rerr := svc.GetAuthenticationOptions()
			if rerr != nil {
				return nil, rerr
			}

			region, ok := authOpts.Get("Region")
			if !ok {
				return nil, fail.SyntaxError("region value unset")
			}

			svcName, xerr := svc.GetName()
			if xerr != nil {
				return nil, xerr
			}

			folder := fmt.Sprintf("images/%s/%s", svcName, region)

			imageList, err := db.ReadAll(folder)
			if err != nil {
				if force {
					logrus.Warnf(
						"Problem creating / accessing Scanner database, ignoring GPU and Freq parameters for now...: %v",
						err,
					)
				} else {
					var noHostError string
					if sizing.MinCPUFreq <= 0 {
						noHostError = fmt.Sprintf(
							"Unable to create a host with '%d' GPUs, problem accessing Scanner database: %v",
							sizing.MinGPU, err,
						)
					} else {
						noHostError = fmt.Sprintf(
							"Unable to create a host with '%d' GPUs and '%.01f' MHz clock frequency, problem accessing Scanner database: %v",
							sizing.MinGPU, sizing.MinCPUFreq, err,
						)
					}
					logrus.Error(noHostError)
					return nil, fail.NewError(noHostError)
				}
			} else {
				var images []abstract.StoredCPUInfo
				for _, f := range imageList {
					imageFound := abstract.StoredCPUInfo{}
					if err := json.Unmarshal([]byte(f), &imageFound); err != nil {
						return nil, fail.Wrap(err, "error unmarshalling image '%s'")
					}

					// if the user asked explicitly no gpu
					if sizing.MinGPU == 0 && imageFound.GPU != 0 {
						continue
					}

					if imageFound.GPU < sizing.MinGPU {
						continue
					}

					if imageFound.CPUFrequency < float64(sizing.MinCPUFreq) {
						continue
					}

					images = append(images, imageFound)
				}

				if !force && (len(images) == 0) {
					var noHostError string
					if sizing.MinCPUFreq <= 0 {
						noHostError = fmt.Sprintf(
							"Unable to create a host with '%d' GPUs, no images matching requirements", sizing.MinGPU,
						)
					} else {
						noHostError = fmt.Sprintf(
							"Unable to create a host with '%d' GPUs and a CPU clock frequencyof '%.01f MHz', no images matching requirements",
							sizing.MinGPU, sizing.MinCPUFreq,
						)
					}
					logrus.Error(noHostError)
					return nil, fail.NewError(noHostError)
				}

				for _, image := range images {
					scannerTpls[image.TemplateID] = true
				}
			}
		}
	}

	reducedTmpls := svc.reduceTemplates(allTpls, svc.whitelistTemplateREs, svc.blacklistTemplateREs)
	if sizing.MinGPU < 1 {
		// Force filtering of known templates with GPU from template list when sizing explicitly asks for no GPU
		gpus, xerr := svc.GetRegexpsOfTemplatesWithGPU()
		if xerr != nil {
			return nil, xerr
		}
		reducedTmpls = svc.reduceTemplates(reducedTmpls, nil, gpus)
	}

	if sizing.MinCores == 0 && sizing.MaxCores == 0 && sizing.MinRAMSize == 0 && sizing.MaxRAMSize == 0 {
		logrus.Debugf("Looking for a host template as small as possible")
	} else {
		coreMsg := ""
		if sizing.MinCores > 0 {
			if sizing.MaxCores > 0 {
				coreMsg = fmt.Sprintf("between %d and %d", sizing.MinCores, sizing.MaxCores)
			} else {
				coreMsg = fmt.Sprintf("at least %d", sizing.MinCores)
			}
		} else {
			coreMsg = fmt.Sprintf("at most %d", sizing.MaxCores)
		}
		ramMsg := ""
		if sizing.MinRAMSize > 0 {
			if sizing.MaxRAMSize > 0 {
				ramMsg = fmt.Sprintf("between %.01f and %.01f", sizing.MinRAMSize, sizing.MaxRAMSize)
			} else {
				ramMsg = fmt.Sprintf("at least %.01f", sizing.MinRAMSize)
			}
		} else {
			coreMsg = fmt.Sprintf("at most %.01f", sizing.MaxRAMSize)
		}
		diskMsg := ""
		if sizing.MinDiskSize > 0 {
			diskMsg = fmt.Sprintf(" and at least %d GB of disk", sizing.MinDiskSize)
		}
		gpuMsg := ""
		if sizing.MinGPU >= 0 {
			gpuMsg = fmt.Sprintf("%d GPU%s", sizing.MinGPU, strprocess.Plural(uint(sizing.MinGPU)))
		}
		logrus.Debugf(
			fmt.Sprintf(
				"Looking for a host template with: %s cores, %s RAM, %s%s", coreMsg, ramMsg, gpuMsg, diskMsg,
			),
		)
	}

	for _, t := range reducedTmpls {
		msg := fmt.Sprintf(
			"Discarded host template '%s' with %d cores, %.01f GB of RAM, %d GPU and %d GB of Disk:", t.Name, t.Cores,
			t.RAMSize, t.GPUNumber, t.DiskSize,
		)
		msg += " %s"
		if sizing.MinCores > 0 && t.Cores < sizing.MinCores {
			logrus.Tracef(msg, "not enough cores")
			continue
		}
		if sizing.MaxCores > 0 && t.Cores > sizing.MaxCores {
			logrus.Tracef(msg, "too many cores")
			continue
		}
		if sizing.MinRAMSize > 0.0 && t.RAMSize < sizing.MinRAMSize {
			logrus.Tracef(msg, "not enough RAM")
			continue
		}
		if sizing.MaxRAMSize > 0.0 && t.RAMSize > sizing.MaxRAMSize {
			logrus.Tracef(msg, "too many RAM")
			continue
		}
		if t.DiskSize > 0 && sizing.MinDiskSize > 0 && t.DiskSize < sizing.MinDiskSize {
			logrus.Tracef(msg, "not enough disk")
			continue
		}
		if (sizing.MinGPU <= 0 && t.GPUNumber > 0) || (sizing.MinGPU > 0 && t.GPUNumber > sizing.MinGPU) {
			logrus.Tracef(msg, "too many GPU")
			continue
		}

		if _, ok := scannerTpls[t.ID]; (ok || !askedForSpecificScannerInfo) && t.ID != "" {
			newT := t
			selectedTpls = append(selectedTpls, &newT)
		}
	}

	sort.Sort(ByRankDRF(selectedTpls))
	return selectedTpls, nil
}

type scoredImage struct {
	abstract.Image
	score float64
}

type scoredImages []scoredImage

func (a scoredImages) Len() int           { return len(a) }
func (a scoredImages) Swap(i, j int)      { a[i], a[j] = a[j], a[i] }
func (a scoredImages) Less(i, j int) bool { return a[i].score < a[j].score }

// FilterImages search an images corresponding to OS Name
func (svc service) FilterImages(filter string) ([]abstract.Image, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	imgs, err := svc.ListImages(false)
	if err != nil {
		return nil, err
	}
	imgs = svc.reduceImages(imgs)

	if len(filter) == 0 {
		return imgs, nil
	}
	var simgs []scoredImage
	// fields := strings.Split(strings.ToUpper(osname), " ")
	for _, img := range imgs {
		// score := 1 / float64(smetrics.WagnerFischer(strings.ToUpper(img.Name), strings.ToUpper(osname), 1, 1, 2))
		score := smetrics.JaroWinkler(strings.ToUpper(img.Name), strings.ToUpper(filter), 0.7, 5)
		// score := matchScore(fields, strings.ToUpper(img.Name))
		// score := SimilarityScore(filter, img.Name)
		if score > 0.5 {
			simgs = append(
				simgs, scoredImage{
					Image: img,
					score: score,
				},
			)
		}

	}
	var fimgs []abstract.Image
	sort.Sort(scoredImages(simgs))
	for _, simg := range simgs {
		fimgs = append(fimgs, simg.Image)
	}

	return fimgs, nil

}

func (svc service) reduceImages(imgs []abstract.Image) []abstract.Image {
	var finalFilter *imagefilters.Filter
	if len(svc.whitelistImageREs) > 0 {
		finalFilter = imagefilters.NewFilter(filterImagesByRegexSlice(svc.whitelistImageREs))
	}
	if len(svc.blacklistImageREs) > 0 {
		blackFilter := imagefilters.NewFilter(filterImagesByRegexSlice(svc.blacklistImageREs))
		if finalFilter == nil {
			finalFilter = blackFilter.Not()
		} else {
			finalFilter = finalFilter.And(blackFilter.Not())
		}
	}
	if finalFilter != nil {
		// templateFilter := templatefilters.NewFilter(finalFilter)
		return imagefilters.FilterImages(imgs, finalFilter)
	}
	return imgs
}

func filterImagesByRegexSlice(res []*regexp.Regexp) imagefilters.Predicate {
	return func(img abstract.Image) bool {
		for _, re := range res {
			if re.Match([]byte(img.Name)) {
				return true
			}
		}
		return false

	}
}

// ListImages reduces the list of needed, if all bool is true, all images are returned, if not, images are filtered using blacklists and whitelists
func (svc service) ListImages(all bool) ([]abstract.Image, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	imgs, err := svc.Provider.ListImages(all)
	if err != nil {
		return nil, err
	}
	return svc.reduceImages(imgs), nil
}

// SearchImage search an image corresponding to OS Name
func (svc service) SearchImage(osname string) (*abstract.Image, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	if osname == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("osname")
	}

	imgs, xerr := svc.ListImages(false)
	if xerr != nil {
		return nil, xerr
	}
	if len(imgs) == 0 {
		return nil, fail.NotFoundError("unable to find an image matching '%s'", osname)
	}

	reg := regexp.MustCompile("[^A-Z0-9]")

	var maxLength int
	for _, img := range imgs {
		length := len(img.Name)
		if maxLength < length {
			maxLength = length
		}
	}

	normalizedOSName := normalizeString(osname, reg)
	paddedNormalizedOSName := addPadding(normalizedOSName, maxLength)

	minWFScore := -1
	wfSelect := -1
	for i, entry := range imgs {
		normalizedImageName := normalizeString(entry.Name, reg)
		normalizedImageName = addPadding(normalizedImageName, maxLength)
		if strings.Contains(normalizedImageName, normalizedOSName) {
			wfScore := smetrics.WagnerFischer(paddedNormalizedOSName, normalizedImageName, 1, 1, 2)
			logrus.Tracef("%*s (%s): WagnerFischerScore:%4d", maxLength, entry.Name, normalizedImageName, wfScore)

			if minWFScore == -1 || wfScore < minWFScore {
				minWFScore = wfScore
				wfSelect = i
			}
		}
	}

	if wfSelect < 0 {
		return nil, fail.NotFoundError("unable to find an image matching '%s'", osname)
	}

	logrus.Infof("Selected image: '%s' (ID='%s')", imgs[wfSelect].Name, imgs[wfSelect].ID)
	return &imgs[wfSelect], nil
}

func normalizeString(in string, reg *regexp.Regexp) string {
	in = strings.ToUpper(in)
	in = reg.ReplaceAllString(in, "")
	return in
}

func addPadding(in string, maxLength int) string {
	if maxLength <= 0 {
		return in
	}

	length := len(in)
	if length < maxLength {
		paddingRight := maxLength - length
		in += strings.Repeat(" ", paddingRight)
	}
	return in
}

// CreateHostWithKeyPair creates a host
func (svc service) CreateHostWithKeyPair(request abstract.HostRequest) (*abstract.HostFull, *userdata.Content, *abstract.KeyPair, fail.Error) {
	if svc.IsNull() {
		return nil, nil, nil, fail.InvalidInstanceError()
	}

	ah := abstract.NewHostCore()
	ah.Name = request.ResourceName
	_, rerr := svc.InspectHost(ah)
	if rerr == nil {
		return nil, nil, nil, abstract.ResourceDuplicateError("host", request.ResourceName)
	}

	// Create temporary key pair
	kpNameuuid, err := uuid.NewV4()
	if err != nil {
		return nil, nil, nil, fail.ConvertError(err)
	}

	kpName := kpNameuuid.String()
	kp, rerr := svc.CreateKeyPair(kpName)
	if rerr != nil {
		return nil, nil, nil, rerr
	}

	// Create host
	hostReq := abstract.HostRequest{
		ResourceName:   request.ResourceName,
		HostName:       request.HostName,
		ImageID:        request.ImageID,
		ImageRef:       request.ImageID,
		KeyPair:        kp,
		PublicIP:       request.PublicIP,
		Subnets:        request.Subnets,
		DefaultRouteIP: request.DefaultRouteIP,
		// DefaultGateway: request.DefaultGateway,
		TemplateID: request.TemplateID,
	}
	host, userData, rerr := svc.CreateHost(hostReq)
	if rerr != nil {
		return nil, nil, nil, rerr
	}
	return host, userData, kp, nil
}

// ListHostsByName list hosts by name
func (svc service) ListHostsByName(details bool) (map[string]*abstract.HostFull, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}

	hosts, err := svc.ListHosts(details)
	if err != nil {
		return nil, err
	}
	hostMap := make(map[string]*abstract.HostFull)
	for _, host := range hosts {
		hostMap[host.Core.Name] = host
	}
	return hostMap, nil
}

// TenantCleanup removes everything related to SafeScale from tenant (mainly metadata)
// if force equals false and there is metadata, returns an error
// WARNING: !!! this will make SafeScale unable to handle the resources !!!
func (svc service) TenantCleanup(force bool) fail.Error {
	if svc.IsNull() {
		return fail.InvalidInstanceError()
	}

	return fail.NotImplementedError("service.TenantCleanup() not yet implemented")
}

// LookupRuleInSecurityGroup checks if a rule is already in Security Group rules
func (svc service) LookupRuleInSecurityGroup(
	asg *abstract.SecurityGroup, rule *abstract.SecurityGroupRule,
) (bool, fail.Error) {
	if asg.IsNull() {
		return false, fail.InvalidParameterError("asg", "cannot be null value of '*abstract.SecurityGroup'")
	}

	_, xerr := asg.Rules.IndexOfEquivalentRule(rule)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			return false, nil
		default:
			return false, xerr
		}
	}
	return true, nil
}

// InspectHostByName hides the "complexity" of the way to get Host by name
func (svc service) InspectHostByName(name string) (*abstract.HostFull, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	return svc.InspectHost(abstract.NewHostCore().SetName(name))
}

// InspectSecurityGroupByName hides the "complexity" of the way to get Security Group by name
func (svc service) InspectSecurityGroupByName(networkID, name string) (*abstract.SecurityGroup, fail.Error) {
	if svc.IsNull() {
		return nil, fail.InvalidInstanceError()
	}
	return svc.InspectSecurityGroup(abstract.NewSecurityGroup().SetName(name).SetNetworkID(networkID))
}

func (svc service) ObjectStorageConfiguration() objectstorage.Config {
	return svc.Location.Configuration()
}
