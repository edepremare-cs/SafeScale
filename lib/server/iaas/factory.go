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
	"bytes"
	"expvar"
	"fmt"
	"regexp"
	"sync"

	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/CS-SI/SafeScale/lib/server/iaas/objectstorage"
	"github.com/CS-SI/SafeScale/lib/server/iaas/providers"
	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/utils/crypt"
	"github.com/CS-SI/SafeScale/lib/utils/data/json"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

var (
	allProviders = map[string]Service{}
	allTenants   = map[string]string{}
)

// Register a Client referenced by the provider name. Ex: "ovh", ovh.New()
// This function shoud be called by the init function of each provider to be registered in SafeScale
func Register(name string, provider providers.Provider) {
	// if already registered, leave
	if _, ok := allProviders[name]; ok {
		return
	}
	allProviders[name] = &service{
		Provider: provider,
	}
}

// GetTenantNames returns all known tenants names
func GetTenantNames() (map[string]string, fail.Error) {
	err := loadConfig()
	return allTenants, err
}

// GetTenants returns all known tenants
func GetTenants() ([]map[string]interface{}, fail.Error) {
	tenants, _, err := getTenantsFromCfg()
	if err != nil {
		return nil, err
	}
	return tenants, err
}

// UseService return the service referenced by the given name.
// If necessary, this function try to load service from configuration file
func UseService(tenantName, metadataVersion string) (newService Service, xerr fail.Error) {
	defer fail.OnExitLogError(&xerr)
	defer fail.OnPanic(&xerr)

	tenants, _, err := getTenantsFromCfg()
	if err != nil {
		return NullService(), err
	}

	var (
		tenantInCfg    bool
		found          bool
		name, provider string
		svc            Service
		svcProvider    = "__not_found__"
	)

	for _, tenant := range tenants {
		name, found = tenant["name"].(string)
		if !found {
			logrus.Error("tenant found without 'name'")
			continue
		}
		if name != tenantName {
			continue
		}

		tenantInCfg = true
		provider, found = tenant["provider"].(string)
		if !found {
			provider, found = tenant["client"].(string)
			if !found {
				logrus.Error("Missing field 'provider' in tenant")
				continue
			}
		}

		svcProvider = provider
		svc, found = allProviders[provider]
		if !found {
			logrus.Errorf("failed to find client '%s' for tenant '%s'", svcProvider, name)
			continue
		}

		_, found = tenant["identity"].(map[string]interface{})
		if !found {
			logrus.Debugf("No section 'identity' found in tenant '%s', continuing.", name)
		}
		_, found = tenant["compute"].(map[string]interface{})
		if !found {
			logrus.Debugf("No section 'compute' found in tenant '%s', continuing.", name)
		}
		_, found = tenant["network"].(map[string]interface{})
		if !found {
			logrus.Debugf("No section 'network' found in tenant '%s', continuing.", name)
		}

		_, tenantObjectStorageFound := tenant["objectstorage"]
		_, tenantMetadataFound := tenant["metadata"]

		// Initializes Provider
		providerInstance, xerr := svc.Build(tenant)
		if xerr != nil {
			return NullService(), fail.Wrap(xerr, "error initializing tenant '%s' on provider '%s'", tenantName, provider)
		}

		newS := &service{
			Provider:   providerInstance,
			cache:      serviceCache{map[string]*ResourceCache{}},
			cacheLock:  &sync.Mutex{},
			tenantName: tenantName,
		}

		// allRegions, xerr := newS.ListRegions()
		// if xerr != nil {
		// 	switch xerr.(type) {
		// 	case *fail.ErrNotFound:
		// 		break
		// 	default:
		// 		return NullService(), xerr
		// 	}
		// }

		authOpts, xerr := providerInstance.GetAuthenticationOptions()
		if xerr != nil {
			return NullService(), xerr
		}

		// Validate region parameter in compute section
		// VPL: does not work with Outscale "cloudgouv"...
		// computeRegion := authOpts.GetString("Region")
		// xerr = validateRegionName(computeRegion, allRegions)
		// if xerr != nil {
		// 	return NullService(), fail.Wrap(xerr, "invalid region in section 'compute'")
		// }

		// Initializes Object Storage
		var objectStorageLocation objectstorage.Location
		if tenantObjectStorageFound {
			objectStorageConfig, xerr := initObjectStorageLocationConfig(authOpts, tenant)
			if xerr != nil {
				return NullService(), xerr
			}

			// VPL: disable region validation, may need to update allRegions for objectstorage/metadata)
			// xerr = validateRegionName(objectStorageConfig.Region, allRegions)
			// if xerr != nil {
			// 	return nil, fail.Wrap(xerr, "invalid region in section 'objectstorage")
			// }

			objectStorageLocation, xerr = objectstorage.NewLocation(objectStorageConfig)
			if xerr != nil {
				return NullService(), fail.Wrap(xerr, "error connecting to Object Storage location")
			}
		} else {
			logrus.Warnf("missing section 'objectstorage' in configuration file for tenant '%s'", tenantName)
		}

		// Initializes Metadata Object Storage (maybe different from the Object Storage)
		var (
			metadataBucket   abstract.ObjectStorageBucket
			metadataCryptKey *crypt.Key
		)
		if tenantMetadataFound || tenantObjectStorageFound {
			metadataLocationConfig, err := initMetadataLocationConfig(authOpts, tenant)
			if err != nil {
				return NullService(), err
			}

			// VPL: disable region validation, may need to update allRegions for objectstorage/metadata)
			// xerr = validateRegionName(metadataLocationConfig.Region, allRegions)
			// if xerr != nil {
			// 	return nil, fail.Wrap(xerr, "invalid region in section 'metadata'")
			// }

			metadataLocation, err := objectstorage.NewLocation(metadataLocationConfig)
			if err != nil {
				return NullService(), fail.Wrap(err, "error connecting to Object Storage location to store metadata")
			}

			if metadataLocationConfig.BucketName == "" {
				serviceCfg, xerr := providerInstance.GetConfigurationOptions()
				if xerr != nil {
					return NullService(), xerr
				}

				anon, found := serviceCfg.Get("MetadataBucketName")
				if !found {
					return NullService(), fail.SyntaxError("missing configuration option 'MetadataBucketName'")
				}
				var ok bool
				metadataLocationConfig.BucketName, ok = anon.(string)
				if !ok {
					return NullService(), fail.InvalidRequestError("invalid bucket name, it's not a string")
				}
			}
			found, err = metadataLocation.FindBucket(metadataLocationConfig.BucketName)
			if err != nil {
				return NullService(), fail.Wrap(err, "error accessing metadata location: %s", metadataLocationConfig.BucketName)
			}

			if found {
				metadataBucket, err = metadataLocation.InspectBucket(metadataLocationConfig.BucketName)
				if err != nil {
					return NullService(), err
				}
			} else {
				// create bucket
				metadataBucket, err = metadataLocation.CreateBucket(metadataLocationConfig.BucketName)
				if err != nil {
					return NullService(), err
				}

				// Creates metadata version file
				if metadataVersion != "" {
					content := bytes.NewBuffer([]byte(metadataVersion))
					_, xerr := metadataLocation.WriteObject(metadataLocationConfig.BucketName, "version", content, int64(content.Len()), nil)
					if xerr != nil {
						return NullService(), fail.Wrap(xerr, "failed to create version object in metadata Bucket")
					}
				}
			}
			if metadataConfig, ok := tenant["metadata"].(map[string]interface{}); ok {
				if key, ok := metadataConfig["CryptKey"].(string); ok {
					ek, err := crypt.NewEncryptionKey([]byte(key))
					if err != nil {
						return NullService(), fail.ConvertError(err)
					}
					metadataCryptKey = ek
				}
			}
			logrus.Infof("Setting default Tenant to '%s'; storing metadata in bucket '%s'", tenantName, metadataBucket.GetName())
		} else {
			return NullService(), fail.SyntaxError("failed to build service: 'metadata' section (and 'objectstorage' as fallback) is missing in configuration file for tenant '%s'", tenantName)
		}

		// service is ready
		newS.Location = objectStorageLocation
		newS.metadataBucket = metadataBucket
		newS.metadataKey = metadataCryptKey

		if xerr := validateRegexps(newS, tenant); xerr != nil {
			return NullService(), xerr
		}

		// increase tenant counter
		ts := expvar.Get("tenant.setted")
		if ts != nil {
			tsi, ok := ts.(*expvar.Int)
			if ok {
				tsi.Add(1)
			}
		}

		return newS, nil
	}

	if !tenantInCfg {
		return NullService(), fail.NotFoundError("tenant '%s' not found in configuration", tenantName)
	}
	return NullService(), fail.NotFoundError("provider builder for '%s'", svcProvider)
}

// validateRegionName validates the availability of the region passed as parameter
func validateRegionName(name string, allRegions []string) fail.Error { // nolint
	// FIXME: Use this function
	if len(allRegions) > 0 {
		regionIsValidInput := false
		for _, vr := range allRegions {
			if name == vr {
				regionIsValidInput = true
			}
		}
		if !regionIsValidInput {
			return fail.NotFoundError("region '%s' not found", name)
		}
	}

	return nil
}

// validateRegexps validates regexp values from tenants file
func validateRegexps(svc *service, tenant map[string]interface{}) fail.Error {
	compute, ok := tenant["compute"].(map[string]interface{})
	if !ok {
		return fail.InvalidParameterError("tenant['compute']", "is not a map")
	}

	res, xerr := validateRegexpsOfKeyword("WhilelistTemplateRegexp", compute["WhitelistTemplateRegexp"])
	if xerr != nil {
		return xerr
	}
	svc.whitelistTemplateREs = res

	res, xerr = validateRegexpsOfKeyword("BlacklistTemplateRegexp", compute["BlacklistTemplateRegexp"])
	if xerr != nil {
		return xerr
	}
	svc.blacklistTemplateREs = res

	res, xerr = validateRegexpsOfKeyword("WhilelistImageRegexp", compute["WhitelistImageRegexp"])
	if xerr != nil {
		return xerr
	}
	svc.whitelistImageREs = res

	res, xerr = validateRegexpsOfKeyword("BlacklistImageRegexp", compute["BlacklistImageRegexp"])
	if xerr != nil {
		return xerr
	}
	svc.blacklistImageREs = res

	return nil
}

// validateRegexpsOfKeyword reads the content of the keyword passed as parameter and returns an array of compiled regexps
func validateRegexpsOfKeyword(keyword string, content interface{}) (out []*regexp.Regexp, _ fail.Error) {
	var emptySlice []*regexp.Regexp

	if str, ok := content.(string); ok {
		re, err := regexp.Compile(str)
		if err != nil {
			return emptySlice, fail.SyntaxError("invalid value '%s' for keyword '%s': %s", str, keyword, err.Error())
		}
		out = append(out, re)
		return out, nil
	}

	if list, ok := content.([]interface{}); ok {
		for _, v := range list {
			re, err := regexp.Compile(v.(string))
			if err != nil {
				return emptySlice, fail.SyntaxError("invalid value '%s' for keyword '%s': %s", v, keyword, err.Error())
			}
			out = append(out, re)
		}
		return out, nil
	}

	return out, nil
}

// initObjectStorageLocationConfig initializes objectstorage.Config struct with map
func initObjectStorageLocationConfig(authOpts providers.Config, tenant map[string]interface{}) (objectstorage.Config, fail.Error) {
	var (
		config objectstorage.Config
		ok     bool
	)

	identity, _ := tenant["identity"].(map[string]interface{})      // nolint
	compute, _ := tenant["compute"].(map[string]interface{})        // nolint
	ostorage, _ := tenant["objectstorage"].(map[string]interface{}) // nolint

	if config.Type, ok = ostorage["Type"].(string); !ok {
		return config, fail.SyntaxError("missing setting 'Type' in 'objectstorage' section")
	}

	if config.Domain, ok = ostorage["Domain"].(string); !ok {
		if config.Domain, ok = ostorage["DomainName"].(string); !ok {
			if config.Domain, ok = compute["Domain"].(string); !ok {
				if config.Domain, ok = compute["DomainName"].(string); !ok {
					if config.Domain, ok = identity["Domain"].(string); !ok {
						if config.Domain, ok = identity["DomainName"].(string); !ok {
							config.Domain = authOpts.GetString("DomainName")
						}
					}
				}
			}
		}
	}
	config.TenantDomain = config.Domain

	if config.Tenant, ok = ostorage["Tenant"].(string); !ok {
		if config.Tenant, ok = ostorage["ProjectName"].(string); !ok {
			if config.Tenant, ok = ostorage["ProjectID"].(string); !ok {
				if config.Tenant, ok = compute["ProjectName"].(string); !ok {
					if config.Tenant, ok = compute["ProjectID"].(string); !ok {
						config.Tenant = authOpts.GetString("ProjectName")
					}
				}
			}
		}
	}

	config.AuthURL, _ = ostorage["AuthURL"].(string)   // nolint
	config.Endpoint, _ = ostorage["Endpoint"].(string) // nolint

	if config.User, ok = ostorage["AccessKey"].(string); !ok {
		if config.User, ok = ostorage["OpenStackID"].(string); !ok {
			if config.User, ok = ostorage["Username"].(string); !ok {
				if config.User, ok = identity["OpenstackID"].(string); !ok {
					config.User, _ = identity["Username"].(string) // nolint
				}
			}
		}
	}

	if config.Key, ok = ostorage["ApplicationKey"].(string); !ok {
		config.Key, _ = identity["ApplicationKey"].(string) // nolint
	}

	if config.SecretKey, ok = ostorage["SecretKey"].(string); !ok {
		if config.SecretKey, ok = ostorage["OpenstackPassword"].(string); !ok {
			if config.SecretKey, ok = ostorage["Password"].(string); !ok {
				if config.SecretKey, ok = identity["SecretKey"].(string); !ok {
					if config.SecretKey, ok = identity["OpenstackPassword"].(string); !ok {
						config.SecretKey, _ = identity["Password"].(string) // nolint
					}
				}
			}
		}
	}

	if config.Region, ok = ostorage["Region"].(string); !ok {
		config.Region, _ = compute["Region"].(string) // nolint
		// if err := validateOVHObjectStorageRegionNaming("objectstorage", config.Region, config.AuthURL); err != nil {
		// 	return config, err
		// }
	}

	if config.AvailabilityZone, ok = ostorage["AvailabilityZone"].(string); !ok {
		config.AvailabilityZone, _ = compute["AvailabilityZone"].(string) // nolint
	}

	// FIXME: Remove google custom code
	if config.Type == "google" {
		keys := []string{"project_id", "private_key_id", "private_key", "client_email", "client_id", "auth_uri", "token_uri", "auth_provider_x509_cert_url", "client_x509_cert_url"}
		for _, key := range keys {
			if _, ok = identity[key].(string); !ok {
				return config, fail.SyntaxError("problem parsing %s", key)
			}
		}

		config.ProjectID, ok = identity["project_id"].(string)
		if !ok {
			return config, fail.NewError("'project_id' MUST be a string in tenants.toml: %v", identity["project_id"])
		}

		googleCfg := stacks.GCPConfiguration{
			Type:         "service_account",
			ProjectID:    identity["project_id"].(string),
			PrivateKeyID: identity["private_key_id"].(string),
			PrivateKey:   identity["private_key"].(string),
			ClientEmail:  identity["client_email"].(string),
			ClientID:     identity["client_id"].(string),
			AuthURI:      identity["auth_uri"].(string),
			TokenURI:     identity["token_uri"].(string),
			AuthProvider: identity["auth_provider_x509_cert_url"].(string),
			ClientCert:   identity["client_x509_cert_url"].(string),
		}

		d1, jserr := json.MarshalIndent(googleCfg, "", "  ")
		if jserr != nil {
			return config, fail.ConvertError(jserr)
		}

		config.Credentials = string(d1)
	}
	return config, nil
}

// func validateOVHObjectStorageRegionNaming(context, region, authURL string) fail.Error {
// 	// If AuthURL contains OVH, special treatment due to change in object storage 'region'-ing since 2020/02/17
// 	// Object Storage regions don't contain anymore an index like compute regions
// 	if strings.Contains(authURL, "ovh.") {
// 		rLen := len(region)
// 		if _, err := strconv.Atoi(region[rLen-1:]); err == nil {
// 			region = region[:rLen-1]
// 			return fail.InvalidRequestError(fmt.Sprintf(`region names for OVH Object Storage have changed since 2020/02/17. Please set or update the %s tenant definition with 'Region = "%s"'.`, context, region))
// 		}
// 	}
// 	return nil
// }

// initMetadataLocationConfig initializes objectstorage.Config struct with map
func initMetadataLocationConfig(authOpts providers.Config, tenant map[string]interface{}) (objectstorage.Config, fail.Error) {
	var (
		config objectstorage.Config
		ok     bool
	)

	// FIXME: This code is ancient and doesn't provide nor hints nor protection against formatting

	identity, _ := tenant["identity"].(map[string]interface{})      // nolint
	compute, _ := tenant["compute"].(map[string]interface{})        // nolint
	ostorage, _ := tenant["objectstorage"].(map[string]interface{}) // nolint
	metadata, _ := tenant["metadata"].(map[string]interface{})      // nolint

	if config.Type, ok = metadata["Type"].(string); !ok {
		if config.Type, ok = ostorage["Type"].(string); !ok {
			return config, fail.SyntaxError("missing setting 'Type' in 'metadata' section")
		}
	}

	if config.Domain, ok = metadata["Domain"].(string); !ok {
		if config.Domain, ok = metadata["DomainName"].(string); !ok {
			if config.Domain, ok = ostorage["Domain"].(string); !ok {
				if config.Domain, ok = ostorage["DomainName"].(string); !ok {
					if config.Domain, ok = compute["Domain"].(string); !ok {
						if config.Domain, ok = compute["DomainName"].(string); !ok {
							if config.Domain, ok = identity["Domain"].(string); !ok {
								if config.Domain, ok = identity["DomainName"].(string); !ok {
									config.Domain = authOpts.GetString("DomainName") // nolint
								}
							}
						}
					}
				}
			}
		}
	}
	config.TenantDomain = config.Domain

	if config.Tenant, ok = metadata["Tenant"].(string); !ok {
		if config.Tenant, ok = metadata["ProjectName"].(string); !ok {
			if config.Tenant, ok = metadata["ProjectID"].(string); !ok {
				if config.Tenant, ok = ostorage["Tenant"].(string); !ok {
					if config.Tenant, ok = ostorage["ProjectName"].(string); !ok {
						if config.Tenant, ok = ostorage["ProjectID"].(string); !ok {
							if config.Tenant, ok = compute["Tenant"].(string); !ok {
								if config.Tenant, ok = compute["ProjectName"].(string); !ok {
									config.Tenant, _ = compute["ProjectID"].(string) // nolint
								}
							}
						}
					}
				}
			}
		}
	}

	if config.AuthURL, ok = metadata["AuthURL"].(string); !ok {
		config.AuthURL, _ = ostorage["AuthURL"].(string) // nolint
	}

	if config.Endpoint, ok = metadata["Endpoint"].(string); !ok {
		config.Endpoint, _ = ostorage["Endpoint"].(string) // nolint
	}

	if config.User, ok = metadata["AccessKey"].(string); !ok {
		if config.User, ok = metadata["OpenstackID"].(string); !ok {
			if config.User, ok = metadata["Username"].(string); !ok {
				if config.User, ok = ostorage["AccessKey"].(string); !ok {
					if config.User, ok = ostorage["OpenStackID"].(string); !ok {
						if config.User, ok = ostorage["Username"].(string); !ok {
							if config.User, ok = identity["Username"].(string); !ok {
								config.User, _ = identity["OpenstackID"].(string) // nolint
							}
						}
					}
				}
			}
		}
	}

	config.DNS, _ = compute["DNS"].(string) // nolint

	if config.Key, ok = metadata["ApplicationKey"].(string); !ok {
		if config.Key, ok = ostorage["ApplicationKey"].(string); !ok {
			config.Key, _ = identity["ApplicationKey"].(string) // nolint
		}
	}

	if config.SecretKey, ok = metadata["SecretKey"].(string); !ok {
		if config.SecretKey, ok = metadata["AccessPassword"].(string); !ok {
			if config.SecretKey, ok = metadata["OpenstackPassword"].(string); !ok {
				if config.SecretKey, ok = metadata["Password"].(string); !ok {
					if config.SecretKey, ok = ostorage["SecretKey"].(string); !ok {
						if config.SecretKey, ok = ostorage["AccessPassword"].(string); !ok {
							if config.SecretKey, ok = ostorage["OpenstackPassword"].(string); !ok {
								if config.SecretKey, ok = ostorage["Password"].(string); !ok {
									if config.SecretKey, ok = identity["SecretKey"].(string); !ok {
										if config.SecretKey, ok = identity["AccessPassword"].(string); !ok {
											if config.SecretKey, ok = identity["Password"].(string); !ok {
												config.SecretKey, _ = identity["OpenstackPassword"].(string) // nolint
											}
										}
									}
								}
							}
						}
					}
				}
			}
		}
	}

	if config.Region, ok = metadata["Region"].(string); !ok {
		if config.Region, ok = ostorage["Region"].(string); !ok {
			config.Region, _ = compute["Region"].(string) // nolint
		}
		// FIXME: Wrong, this needs validation, but not ALL providers
		// if err := validateOVHObjectStorageRegionNaming("objectstorage", config.Region, config.AuthURL); err != nil {
		// 	return config, err
		// }
	}

	if config.AvailabilityZone, ok = metadata["AvailabilityZone"].(string); !ok {
		if config.AvailabilityZone, ok = ostorage["AvailabilityZone"].(string); !ok {
			config.AvailabilityZone, _ = compute["AvailabilityZone"].(string) // nolint
		}
	}

	// FIXME: Remove google custom code, it's a problem, think about delegation to providers
	if config.Type == "google" {
		keys := []string{"project_id", "private_key_id", "private_key", "client_email", "client_id", "auth_uri", "token_uri", "auth_provider_x509_cert_url", "client_x509_cert_url"}
		for _, key := range keys {
			if _, ok = identity[key].(string); !ok {
				return config, fail.SyntaxError("problem parsing %s", key)
			}
		}

		config.ProjectID, ok = identity["project_id"].(string)
		if !ok {
			return config, fail.NewError("'project_id' MUST be a string in tenants.toml: %v", identity["project_id"])
		}

		googleCfg := stacks.GCPConfiguration{
			Type:         "service_account",
			ProjectID:    identity["project_id"].(string),
			PrivateKeyID: identity["private_key_id"].(string),
			PrivateKey:   identity["private_key"].(string),
			ClientEmail:  identity["client_email"].(string),
			ClientID:     identity["client_id"].(string),
			AuthURI:      identity["auth_uri"].(string),
			TokenURI:     identity["token_uri"].(string),
			AuthProvider: identity["auth_provider_x509_cert_url"].(string),
			ClientCert:   identity["client_x509_cert_url"].(string),
		}

		d1, jserr := json.MarshalIndent(googleCfg, "", "  ")
		if jserr != nil {
			return config, fail.ConvertError(jserr)
		}

		config.Credentials = string(d1)
	}

	config.BucketName, _ = metadata["MetadataBucketName"].(string) // nolint
	return config, nil
}

func loadConfig() fail.Error {
	tenantsCfg, v, err := getTenantsFromCfg()
	if err != nil {
		return err
	}
	for _, tenant := range tenantsCfg {
		if name, ok := tenant["name"].(string); ok {
			if provider, ok := tenant["client"].(string); ok {
				allTenants[name] = provider
			} else {
				return fail.SyntaxError("invalid configuration file '%s'. Tenant '%s' has no client type", v.ConfigFileUsed(), name)
			}
		} else {
			return fail.SyntaxError("invalid configuration file. A tenant has no 'name' entry in '%s'", v.ConfigFileUsed())
		}
	}
	return nil
}

func getTenantsFromCfg() ([]map[string]interface{}, *viper.Viper, fail.Error) {
	v := viper.New()
	v.AddConfigPath(".")
	v.AddConfigPath("$HOME/.safescale")
	v.AddConfigPath(utils.AbsPathify("$HOME/.safescale"))
	v.AddConfigPath("$HOME/.config/safescale")
	v.AddConfigPath(utils.AbsPathify("$HOME/.config/safescale"))
	v.AddConfigPath("/etc/safescale")
	v.SetConfigName("tenants")

	if err := v.ReadInConfig(); err != nil { // Handle errors reading the config file
		msg := fmt.Sprintf("error reading configuration file: %s", err.Error())
		logrus.Errorf(msg)
		return nil, v, fail.SyntaxError(msg)
	}
	// settings := v.AllSettings()

	var tenantsCfg []map[string]interface{}
	err := v.UnmarshalKey("tenants", &tenantsCfg)
	if err != nil {
		return nil, v, fail.SyntaxError("failed to convert tenants file to map[string]interface{}")
	}

	jsoned, err := json.Marshal(tenantsCfg)
	if err != nil {
		return nil, v, fail.ConvertError(err)
	}

	var out []map[string]interface{}
	err = json.Unmarshal(jsoned, &out)
	if err != nil {
		return nil, v, fail.ConvertError(err)
	}
	return out, v, nil
}
