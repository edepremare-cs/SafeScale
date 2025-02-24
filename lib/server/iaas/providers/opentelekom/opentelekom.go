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

package opentelekom

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/asaskevich/govalidator"
	"github.com/sirupsen/logrus"

	"github.com/CS-SI/SafeScale/lib/server/iaas"
	"github.com/CS-SI/SafeScale/lib/server/iaas/objectstorage"
	"github.com/CS-SI/SafeScale/lib/server/iaas/providers"
	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks"
	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks/api"
	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks/huaweicloud"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/volumespeed"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

const (
	opentelekomDefaultImage = "Ubuntu 20.04"

	identityEndpointTemplate string = "https://iam.%s.otc.t-systems.com"
)

var (
	dnsServers = []string{"1.1.1.1"}
)

// provider is the providerementation of the OpenTelekom provider
type provider struct {
	api.Stack

	templatesWithGPU []string
	tenantParameters map[string]interface{}
}

// New creates a new instance of opentelekom provider
func New() providers.Provider {
	return &provider{}
}

// IsNull returns true if the instance is considered as a null value
func (p *provider) IsNull() bool {
	return p == nil || p.Stack == nil
}

// Build builds a new Client from configuration parameter
func (p *provider) Build(params map[string]interface{}) (providers.Provider, fail.Error) {
	identity, _ := params["identity"].(map[string]interface{}) // nolint
	compute, _ := params["compute"].(map[string]interface{})   // nolint
	network, _ := params["network"].(map[string]interface{})   // nolint

	username, _ := identity["Username"].(string)         // nolint
	password, _ := identity["Password"].(string)         // nolint
	domainName, _ := identity["DomainName"].(string)     // nolint
	projectID, _ := compute["ProjectID"].(string)        // nolint
	region, _ := compute["Region"].(string)              // nolint
	zone, _ := compute["AvailabilityZone"].(string)      // nolint
	vpcName, _ := network["DefaultNetworkName"].(string) // nolint
	vpcCIDR, _ := network["DefaultNetworkCIDR"].(string) // nolint

	identityEndpoint, _ := identity["IdentityEndpoint"].(string) // nolint
	if identityEndpoint == "" {
		identityEndpoint = fmt.Sprintf(identityEndpointTemplate, region)
	}

	operatorUsername := abstract.DefaultUser
	if operatorUsernameIf, ok := compute["OperatorUsername"]; ok {
		operatorUsername, ok = operatorUsernameIf.(string)
		if ok {
			if operatorUsername == "" {
				logrus.Warnf("OperatorUsername is empty ! Check your tenants.toml file ! Using 'safescale' user instead.")
				operatorUsername = abstract.DefaultUser
			}
		}
	}

	defaultImage, _ := compute["DefaultImage"].(string) // nolint
	if defaultImage == "" {
		defaultImage = opentelekomDefaultImage
	}

	maxLifeTime := 0
	if _, ok := compute["MaxLifetimeInHours"].(string); ok {
		maxLifeTime, _ = strconv.Atoi(compute["MaxLifetimeInHours"].(string))
	}

	authOptions := stacks.AuthenticationOptions{
		IdentityEndpoint: identityEndpoint,
		Username:         username,
		Password:         password,
		DomainName:       domainName,
		ProjectID:        projectID,
		Region:           region,
		AvailabilityZone: zone,
		AllowReauth:      true,
	}

	govalidator.TagMap["alphanumwithdashesandunderscores"] = func(str string) bool {
		rxp := regexp.MustCompile(stacks.AlphanumericWithDashesAndUnderscores)
		return rxp.Match([]byte(str))
	}

	_, err := govalidator.ValidateStruct(authOptions)
	if err != nil {
		return nil, fail.ConvertError(err)
	}

	providerName := "huaweicloud"
	metadataBucketName, xerr := objectstorage.BuildMetadataBucketName(providerName, region, domainName, projectID)
	if xerr != nil {
		return nil, xerr
	}

	customDNS, _ := compute["DNS"].(string) //. nolint
	if customDNS != "" {
		if strings.Contains(customDNS, ",") {
			fragments := strings.Split(customDNS, ",")
			for _, fragment := range fragments {
				fragment = strings.TrimSpace(fragment)
				if govalidator.IsIP(fragment) {
					dnsServers = append(dnsServers, fragment)
				}
			}
		} else {
			fragment := strings.TrimSpace(customDNS)
			if govalidator.IsIP(fragment) {
				dnsServers = append(dnsServers, fragment)
			}
		}
	}

	cfgOptions := stacks.ConfigurationOptions{
		DNSList:             dnsServers,
		UseFloatingIP:       true,
		UseLayer3Networking: false,
		VolumeSpeeds: map[string]volumespeed.Enum{
			"SATA": volumespeed.Cold,
			"SAS":  volumespeed.Hdd,
			"Ssd":  volumespeed.Ssd,
		},
		MetadataBucket:     metadataBucketName,
		OperatorUsername:   operatorUsername,
		ProviderName:       providerName,
		DefaultNetworkName: vpcName,
		DefaultNetworkCIDR: vpcCIDR,
		DefaultImage:       defaultImage,
		MaxLifeTime:        maxLifeTime,
	}
	stack, xerr := huaweicloud.New(authOptions, cfgOptions)
	if xerr != nil {
		return nil, xerr
	}

	wrapped := api.StackProxy{
		InnerStack: stack,
		Name:       "opentelekomm",
	}

	newP := provider{
		Stack:            wrapped,
		tenantParameters: params,
	}

	wp := providers.ProviderProxy{
		InnerProvider: &newP,
		Name:          wrapped.Name,
	}

	return wp, nil
}

// ListTemplates ... ; overloads stack.ListTemplates() to allow to filter templates to show
// Value of all has no impact on the result
func (p provider) ListTemplates(all bool) ([]abstract.HostTemplate, fail.Error) {
	if p.IsNull() {
		return []abstract.HostTemplate{}, fail.InvalidInstanceError()
	}
	return p.Stack.(api.ReservedForProviderUse).ListTemplates(all)
}

// ListImages ... ; overloads stack.ListImages() to allow to filter images to show
// Value of all has no impact on the result
func (p provider) ListImages(all bool) ([]abstract.Image, fail.Error) {
	if p.IsNull() {
		return []abstract.Image{}, fail.InvalidInstanceError()
	}
	return p.Stack.(api.ReservedForProviderUse).ListImages(all)
}

// GetAuthenticationOptions returns the auth options
func (p provider) GetAuthenticationOptions() (providers.Config, fail.Error) {
	cfg := providers.ConfigMap{}

	opts, err := p.Stack.(api.ReservedForProviderUse).GetRawAuthenticationOptions()
	if err != nil {
		return nil, err
	}
	cfg.Set("TenantName", opts.TenantName)
	cfg.Set("Login", opts.Username)
	cfg.Set("Password", opts.Password)
	cfg.Set("AuthURL", opts.IdentityEndpoint)
	cfg.Set("Region", opts.Region)

	return cfg, nil
}

// GetConfigurationOptions return configuration parameters
func (p provider) GetConfigurationOptions() (providers.Config, fail.Error) {
	cfg := providers.ConfigMap{}

	opts, err := p.Stack.(api.ReservedForProviderUse).GetRawConfigurationOptions()
	if err != nil {
		return nil, err
	}

	provName, xerr := p.GetName()
	if xerr != nil {
		return nil, xerr
	}

	cfg.Set("DNSList", opts.DNSList)
	cfg.Set("AutoHostNetworkInterfaces", opts.AutoHostNetworkInterfaces)
	cfg.Set("UseLayer3Networking", opts.UseLayer3Networking)
	cfg.Set("DefaultImage", opts.DefaultImage)
	cfg.Set("MetadataBucketName", opts.MetadataBucket)
	cfg.Set("OperatorUsername", opts.OperatorUsername)
	cfg.Set("ProviderName", provName)
	cfg.Set("UseNATService", opts.UseNATService)
	cfg.Set("MaxLifeTimeInHours", opts.MaxLifeTime)

	return cfg, nil
}

// GetName ...
func (p provider) GetName() (string, fail.Error) {
	return "opentelekom", nil
}

// GetStack returns the stack object used by the provider
// Note: use with caution, last resort option
func (p provider) GetStack() (api.Stack, fail.Error) {
	return p.Stack, nil
}

// GetTenantParameters ...
func (p provider) GetTenantParameters() (map[string]interface{}, fail.Error) {
	return p.tenantParameters, nil
}

// GetCapabilities returns the capabilities of the provider
func (p provider) GetCapabilities() (providers.Capabilities, fail.Error) {
	return providers.Capabilities{
		PrivateVirtualIP: true,
	}, nil
}

// GetRegexpsOfTemplatesWithGPU returns a slice of regexps corresponding to templates with GPU
func (p provider) GetRegexpsOfTemplatesWithGPU() ([]*regexp.Regexp, fail.Error) {
	var emptySlice []*regexp.Regexp
	if p.IsNull() {
		return emptySlice, nil
	}

	var (
		out []*regexp.Regexp
	)
	for _, v := range p.templatesWithGPU {
		re, err := regexp.Compile(v)
		if err != nil {
			return emptySlice, nil
		}
		out = append(out, re)
	}

	return out, nil
}

// init registers the opentelekom provider
func init() {
	iaas.Register("opentelekom", &provider{})
}
