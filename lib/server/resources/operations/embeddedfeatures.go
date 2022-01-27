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
	"bytes"
	"crypto/sha256"
	"encoding/hex"

	"github.com/CS-SI/SafeScale/lib/utils/debug"
	rice "github.com/GeertJohan/go.rice"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/CS-SI/SafeScale/lib/server/resources/enums/installmethod"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
)

//go:generate rice embed-go

const featureFileExt = ".yml"

var (
	templateBox *rice.Box
	// emptyParams = map[string]interface{}{}

	availableEmbeddedFeaturesMap = map[installmethod.Enum]map[string]*FeatureFile{}
	allEmbeddedFeaturesMap       = map[string]*FeatureFile{}
	allEmbeddedFeatures          []*FeatureFile
)

func getSHA256Hash(text string) string {
	hasher := sha256.New()
	_, err := hasher.Write([]byte(text))
	if err != nil {
		return ""
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// loadSpecFile returns the content of the spec file of the feature named 'name'
func loadSpecFile(name string) (string, *viper.Viper, error) {
	if templateBox == nil {
		var err error
		templateBox, err = rice.FindBox("../operations/embeddedfeatures")
		err = debug.InjectPlannedError(err)
		if err != nil {
			return "", nil, fail.Wrap(err, "failed to open embedded feature specification MetadataFolder")
		}
	}
	name += featureFileExt
	tmplString, err := templateBox.String(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		return "", nil, fail.Wrap(err, "failed to read embedded feature specification file '%s'", name)
	}

	logrus.Tracef("loaded feature %s:SHA256:%s", name, getSHA256Hash(tmplString))

	v := viper.New()
	v.SetConfigType("yaml")
	err = v.ReadConfig(bytes.NewBuffer([]byte(tmplString)))
	err = debug.InjectPlannedError(err)
	if err != nil {
		return "", nil, fail.Wrap(err, "syntax error in feature specification file '%s'", name)
	}

	// Validating content...
	if !v.IsSet("feature") {
		return "", nil, fail.SyntaxError("feature specification file '%s' must begin with 'feature:'", name)
	}
	if !v.IsSet("feature.install") {
		return "", nil, fail.SyntaxError("syntax error in feature specification file '%s': missing 'install'", name)
	}
	if len(v.GetStringMap("feature.install")) == 0 {
		return "", nil, fail.SyntaxError("syntax error in feature specification file '%s': 'install' defines no method", name)
	}
	return name, v, nil
}

// dockerFeature ...
func dockerFeature() *FeatureFile {
	name := "docker"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}

	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// dockerSwarmFeature ...
func dockerSwarmFeature() *FeatureFile {
	name := "docker-swarm"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// ntpServerFeature ...
func ntpServerFeature() *FeatureFile {
	name := "ntpserver"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// ntpServerFeature ...
func ntpClientFeature() *FeatureFile {
	name := "ntpclient"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// ansibleFeature from official repos ...
func ansibleFeature() *FeatureFile {
	name := "ansible"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// ansibleForClusterFeature  ...
func ansibleForClusterFeature() *FeatureFile {
	name := "ansible-for-cluster"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// certificateAuthorityFeature from official repos ...
func certificateAuthorityFeature() *FeatureFile {
	name := "certificateauthority"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// nVidiaDockerFeature ...
func nVidiaDockerFeature() *FeatureFile {
	name := "nvidiadocker"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// kubernetesFeature ...
func kubernetesFeature() *FeatureFile {
	name := "kubernetes"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// helm2Feature ...
func helm2Feature() *FeatureFile {
	name := "helm2"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// helm3Feature ...
func helm3Feature() *FeatureFile {
	name := "helm3"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// remoteDesktopFeature ...
func remoteDesktopFeature() *FeatureFile {
	name := "remotedesktop"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// proxycacheServerFeature ...
func proxycacheServerFeature() *FeatureFile {
	name := "proxycache-server"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// proxycacheClientFeature ...
func proxycacheClientFeature() *FeatureFile {
	name := "proxycache-client"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// postgres4gatewayFeature ...
func postgres4gatewayFeature() *FeatureFile {
	name := "postgres4gateway"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// edgeproxy4subnetFeature ...
func edgeproxy4subnetFeature() *FeatureFile {
	name := "edgeproxy4subnet"
	filename, specs, err := loadSpecFile(name)
	err = debug.InjectPlannedError(err)
	if err != nil {
		panic(err.Error())
	}
	return &FeatureFile{
		displayName: name,
		fileName:    filename,
		embedded:    true,
		specs:       specs,
	}
}

// NOTE: init() moved in zinit.go, to be sure the init() of rice-box.go is called first
