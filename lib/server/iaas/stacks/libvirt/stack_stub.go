//+build !libvirt

/*
 * Copyright 2018, CS Systemes d'Information, http://www.c-s.fr
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

package local

import (
	"fmt"
	"time"

	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks"
	"github.com/CS-SI/SafeScale/lib/server/iaas/userdata"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstracts"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/hoststate"
)

var errorStr = "libvirt Driver is not enabled, use the libvirt option while compiling (make libvirt all)"

// Stack is the implementation of the local driver regarding to the api.ClientAPI
type Stack struct {
}

// WaitHostReady ...
func (s *Stack) WaitHostReady(hostParam interface{}, timeout time.Duration) (*abstracts.Host, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListAvailabilityZones stub
func (s *Stack) ListAvailabilityZones() (map[string]bool, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListRegions stub
func (s *Stack) ListRegions() ([]string, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListImages stub
func (s *Stack) ListImages(all bool) ([]abstracts.Image, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetImage stub
func (s *Stack) GetImage(id string) (*abstracts.Image, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetTemplate stub
func (s *Stack) GetTemplate(id string) (*abstracts.HostTemplate, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListTemplates stub
func (s *Stack) ListTemplates(all bool) ([]abstracts.HostTemplate, error) {
	return nil, fmt.Errorf(errorStr)
}

// CreateKeyPair stub
func (s *Stack) CreateKeyPair(name string) (*abstracts.KeyPair, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetKeyPair stub
func (s *Stack) GetKeyPair(id string) (*abstracts.KeyPair, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListKeyPairs stub
func (s *Stack) ListKeyPairs() ([]abstracts.KeyPair, error) {
	return nil, fmt.Errorf(errorStr)
}

// DeleteKeyPair stub
func (s *Stack) DeleteKeyPair(id string) error {
	return fmt.Errorf(errorStr)
}

// CreateNetwork stub
func (s *Stack) CreateNetwork(req abstracts.NetworkRequest) (*abstracts.Network, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetNetwork stub
func (s *Stack) GetNetwork(id string) (*abstracts.Network, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetNetworkByName stub
func (s *Stack) GetNetworkByName(name string) (*abstracts.Network, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListNetworks stub
func (s *Stack) ListNetworks() ([]*abstracts.Network, error) {
	return nil, fmt.Errorf(errorStr)
}

// DeleteNetwork stub
func (s *Stack) DeleteNetwork(id string) error {
	return fmt.Errorf(errorStr)
}

// CreateGateway stub
func (s *Stack) CreateGateway(req abstracts.GatewayRequest) (*abstracts.Host, *userdata.Content, error) {
	return nil, nil, fmt.Errorf(errorStr)
}

// DeleteGateway stub
func (s *Stack) DeleteGateway(string) error {
	return fmt.Errorf(errorStr)
}

// CreateVIP stub
func (s *Stack) CreateVIP(networkID string, description string) (*abstracts.VIP, error) {
	return nil, fmt.Errorf(errorStr)
}

// AddPublicIPToVIP stub
func (s *Stack) AddPublicIPToVIP(vip *abstracts.VIP) error {
	return fmt.Errorf(errorStr)
}

// BindHostToVIP stub
func (s *Stack) BindHostToVIP(vip *abstracts.VIP, host *abstracts.Host) (string, string, error) {
	return "", "", fmt.Errorf(errorStr)
}

// UnbindHostFromVIP stub
func (s *Stack) UnbindHostFromVIP(vip *abstracts.VIP, host *abstracts.Host) error {
	return fmt.Errorf(errorStr)
}

// DeleteVIP stub
func (s *Stack) DeleteVIP(vip *abstracts.VIP) error {
	return fmt.Errorf(errorStr)
}

// CreateHost stub
func (s *Stack) CreateHost(request abstracts.HostRequest) (*abstracts.Host, *userdata.Content, error) {
	return nil, nil, fmt.Errorf(errorStr)
}

// ResizeHost stub
func (s *Stack) ResizeHost(id string, request abstracts.SizingRequirements) (*abstracts.Host, error) {
	return nil, fmt.Errorf(errorStr)
}

// InspectHost stub
func (s *Stack) InspectHost(interface{}) (*abstracts.Host, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetHostByName stub
func (s *Stack) GetHostByName(string) (*abstracts.Host, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetHostState stub
func (s *Stack) GetHostState(interface{}) (hoststate.Enum, error) {
	return hoststate.ERROR, fmt.Errorf(errorStr)
}

// ListHosts stub
func (s *Stack) ListHosts() ([]*abstracts.Host, error) {
	return nil, fmt.Errorf(errorStr)
}

// DeleteHost stub
func (s *Stack) DeleteHost(id string) error {
	return fmt.Errorf(errorStr)
}

// StartHost stub
func (s *Stack) StartHost(id string) error {
	return fmt.Errorf(errorStr)
}

// StopHost stub
func (s *Stack) StopHost(id string) error {
	return fmt.Errorf(errorStr)
}

// RebootHost stub
func (s *Stack) RebootHost(id string) error {
	return fmt.Errorf(errorStr)
}

// CreateVolume stub
func (s *Stack) CreateVolume(request abstracts.VolumeRequest) (*abstracts.Volume, error) {
	return nil, fmt.Errorf(errorStr)
}

// GetVolume stub
func (s *Stack) GetVolume(id string) (*abstracts.Volume, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListVolumes stub
func (s *Stack) ListVolumes() ([]abstracts.Volume, error) {
	return nil, fmt.Errorf(errorStr)
}

// DeleteVolume stub
func (s *Stack) DeleteVolume(id string) error {
	return fmt.Errorf(errorStr)
}

// CreateVolumeAttachment stub
func (s *Stack) CreateVolumeAttachment(request abstracts.VolumeAttachmentRequest) (string, error) {
	return "", fmt.Errorf(errorStr)
}

// GetVolumeAttachment stub
func (s *Stack) GetVolumeAttachment(serverID, id string) (*abstracts.VolumeAttachment, error) {
	return nil, fmt.Errorf(errorStr)
}

// ListVolumeAttachments stub
func (s *Stack) ListVolumeAttachments(serverID string) ([]abstracts.VolumeAttachment, error) {
	return nil, fmt.Errorf(errorStr)
}

// DeleteVolumeAttachment stub
func (s *Stack) DeleteVolumeAttachment(serverID, id string) error {
	return fmt.Errorf(errorStr)
}

// GetConfigurationOptions stub
func (s *Stack) GetConfigurationOptions() stacks.ConfigurationOptions {
	return stacks.ConfigurationOptions{}
}

// GetAuthenticationOptions stub
func (s *Stack) GetAuthenticationOptions() stacks.AuthenticationOptions {
	return stacks.AuthenticationOptions{}
}
