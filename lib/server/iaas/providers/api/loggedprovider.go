package api

import (
	"time"

	"github.com/sirupsen/logrus"

	"github.com/CS-SI/SafeScale/lib/server/iaas/abstract"
	"github.com/CS-SI/SafeScale/lib/server/iaas/abstract/enums/hoststate"
	"github.com/CS-SI/SafeScale/lib/server/iaas/abstract/userdata"
	"github.com/CS-SI/SafeScale/lib/server/iaas/providers"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// LoggedProvider ...
type LoggedProvider WrappedProvider

// Provider specific functions

// Build ...
func (w LoggedProvider) Build(something map[string]interface{}) (Provider, fail.Error) {
	defer w.prepare(w.trace("Build"))
	return w.InnerProvider.Build(something)
}

// ListImages ...
func (w LoggedProvider) ListImages(all bool) ([]abstract.Image, fail.Error) {
	defer w.prepare(w.trace("ListImages"))
	return w.InnerProvider.ListImages(all)
}

// ListTemplates ...
func (w LoggedProvider) ListTemplates(all bool) ([]abstract.HostTemplate, fail.Error) {
	defer w.prepare(w.trace("ListTemplates"))
	return w.InnerProvider.ListTemplates(all)
}

// GetAuthenticationOptions ...
func (w LoggedProvider) GetAuthenticationOptions() (providers.Config, fail.Error) {
	defer w.prepare(w.trace("GetAuthenticationOptions"))
	return w.InnerProvider.GetAuthenticationOptions()
}

// GetConfigurationOptions ...
func (w LoggedProvider) GetConfigurationOptions() (providers.Config, fail.Error) {
	defer w.prepare(w.trace("GetConfigurationOptions"))
	return w.InnerProvider.GetConfigurationOptions()
}

// GetName ...
func (w LoggedProvider) GetName() string {
	defer w.prepare(w.trace("GetName"))
	return w.InnerProvider.GetName()
}

// GetTenantParameters ...
func (w LoggedProvider) GetTenantParameters() map[string]interface{} {
	defer w.prepare(w.trace("GetTenantParameters"))
	return w.InnerProvider.GetTenantParameters()
}

// Stack specific functions

// trace ...
func (w LoggedProvider) trace(s string) (string, time.Time) {
	logrus.Tracef("stacks.%s::%s() called", w.Name, s)
	return s, time.Now()
}

// prepare ...
func (w LoggedProvider) prepare(s string, startTime time.Time) {
	logrus.Tracef("stacks.%s::%s() done in [%s]", w.Name, s, temporal.FormatDuration(time.Since(startTime)))
}

// NewLoggedProvider ...
func NewLoggedProvider(innerProvider Provider, name string) *LoggedProvider {
	return &LoggedProvider{InnerProvider: innerProvider, Name: name}
}

// ListAvailabilityZones ...
func (w LoggedProvider) ListAvailabilityZones() (map[string]bool, fail.Error) {
	defer w.prepare(w.trace("ListAvailabilityZones"))
	return w.InnerProvider.ListAvailabilityZones()
}

// ListRegions ...
func (w LoggedProvider) ListRegions() ([]string, fail.Error) {
	defer w.prepare(w.trace("ListRegions"))
	return w.InnerProvider.ListRegions()
}

// GetImage ...
func (w LoggedProvider) GetImage(id string) (*abstract.Image, fail.Error) {
	defer w.prepare(w.trace("GetImage"))
	return w.InnerProvider.GetImage(id)
}

// GetTemplate ...
func (w LoggedProvider) GetTemplate(id string) (*abstract.HostTemplate, fail.Error) {
	defer w.prepare(w.trace("GetTemplate"))
	return w.InnerProvider.GetTemplate(id)
}

// CreateKeyPair ...
func (w LoggedProvider) CreateKeyPair(name string) (*abstract.KeyPair, fail.Error) {
	defer w.prepare(w.trace("CreateKeyPair"))
	return w.InnerProvider.CreateKeyPair(name)
}

// GetKeyPair ...
func (w LoggedProvider) GetKeyPair(id string) (*abstract.KeyPair, fail.Error) {
	defer w.prepare(w.trace("GetKeyPair"))
	return w.InnerProvider.GetKeyPair(id)
}

// ListKeyPairs ...
func (w LoggedProvider) ListKeyPairs() ([]abstract.KeyPair, fail.Error) {
	defer w.prepare(w.trace("ListKeyPairs"))
	return w.InnerProvider.ListKeyPairs()
}

// DeleteKeyPair ...
func (w LoggedProvider) DeleteKeyPair(id string) error {
	defer w.prepare(w.trace("DeleteKeyPair"))
	return w.InnerProvider.DeleteKeyPair(id)
}

// CreateNetwork ...
func (w LoggedProvider) CreateNetwork(req abstract.NetworkRequest) (*abstract.Network, fail.Error) {
	defer w.prepare(w.trace("CreateNetwork"))
	return w.InnerProvider.CreateNetwork(req)
}

// GetNetwork ...
func (w LoggedProvider) GetNetwork(id string) (*abstract.Network, fail.Error) {
	defer w.prepare(w.trace("GetNetwork"))
	return w.InnerProvider.GetNetwork(id)
}

// GetNetworkByName ...
func (w LoggedProvider) GetNetworkByName(name string) (*abstract.Network, fail.Error) {
	defer w.prepare(w.trace("GetNetworkByName"))
	return w.InnerProvider.GetNetworkByName(name)
}

// ListNetworks ...
func (w LoggedProvider) ListNetworks() ([]*abstract.Network, fail.Error) {
	defer w.prepare(w.trace("ListNetworks"))
	return w.InnerProvider.ListNetworks()
}

// DeleteNetwork ...
func (w LoggedProvider) DeleteNetwork(id string) error {
	defer w.prepare(w.trace("DeleteNetwork"))
	return w.InnerProvider.DeleteNetwork(id)
}

// CreateGateway ...
func (w LoggedProvider) CreateGateway(req abstract.GatewayRequest, sizing *abstract.SizingRequirements) (*abstract.Host, *userdata.Content, fail.Error) {
	defer w.prepare(w.trace("CreateGateway"))
	return w.InnerProvider.CreateGateway(req, sizing)
}

// DeleteGateway ...
func (w LoggedProvider) DeleteGateway(networkID string) error {
	defer w.prepare(w.trace("DeleteGateway"))
	return w.InnerProvider.DeleteGateway(networkID)
}

// CreateVIP ...
func (w LoggedProvider) CreateVIP(networkID string, description string) (*abstract.VirtualIP, fail.Error) {
	defer w.prepare(w.trace("CreateVIP"))
	return w.InnerProvider.CreateVIP(networkID, description)
}

// AddPublicIPToVIP adds a public IP to VIP
func (w LoggedProvider) AddPublicIPToVIP(vip *abstract.VirtualIP) error {
	defer w.prepare(w.trace("AddPublicIPToVIP"))
	return w.InnerProvider.AddPublicIPToVIP(vip)
}

// BindHostToVIP makes the host passed as parameter an allowed "target" of the VIP
func (w LoggedProvider) BindHostToVIP(vip *abstract.VirtualIP, hostID string) error {
	defer w.prepare(w.trace("BindHostToVIP"))
	return w.InnerProvider.BindHostToVIP(vip, hostID)
}

// UnbindHostFromVIP removes the bind between the VIP and a host
func (w LoggedProvider) UnbindHostFromVIP(vip *abstract.VirtualIP, hostID string) error {
	defer w.prepare(w.trace("UnbindHostFromVIP"))
	return w.InnerProvider.UnbindHostFromVIP(vip, hostID)
}

// DeleteVIP deletes the port corresponding to the VIP
func (w LoggedProvider) DeleteVIP(vip *abstract.VirtualIP) error {
	defer w.prepare(w.trace("DeleteVIP"))
	return w.InnerProvider.DeleteVIP(vip)
}

// CreateHost ...
func (w LoggedProvider) CreateHost(request abstract.HostRequest) (*abstract.Host, *userdata.Content, fail.Error) {
	defer w.prepare(w.trace("CreateHost"))
	return w.InnerProvider.CreateHost(request)
}

// InspectHost ...
func (w LoggedProvider) InspectHost(something interface{}) (*abstract.Host, fail.Error) {
	defer w.prepare(w.trace("InspectHost"))
	return w.InnerProvider.InspectHost(something)
}

// GetHostByName ...
func (w LoggedProvider) GetHostByName(name string) (*abstract.Host, fail.Error) {
	defer w.prepare(w.trace("GetHostByName"))
	return w.InnerProvider.GetHostByName(name)
}

// GetHostState ...
func (w LoggedProvider) GetHostState(something interface{}) (hoststate.Enum, fail.Error) {
	defer w.prepare(w.trace("GetHostState"))
	return w.InnerProvider.GetHostState(something)
}

// ListHosts ...
func (w LoggedProvider) ListHosts() ([]*abstract.Host, fail.Error) {
	defer w.prepare(w.trace("ListHosts"))
	return w.InnerProvider.ListHosts()
}

// DeleteHost ...
func (w LoggedProvider) DeleteHost(id string) error {
	defer w.prepare(w.trace("DeleteHost"))
	return w.InnerProvider.DeleteHost(id)
}

// StopHost ...
func (w LoggedProvider) StopHost(id string) error {
	defer w.prepare(w.trace("StopHost"))
	return w.InnerProvider.StopHost(id)
}

// StartHost ...
func (w LoggedProvider) StartHost(id string) error {
	defer w.prepare(w.trace("StartHost"))
	return w.InnerProvider.StartHost(id)
}

// RebootHost ...
func (w LoggedProvider) RebootHost(id string) error {
	defer w.prepare(w.trace("RebootHost"))
	return w.InnerProvider.RebootHost(id)
}

// ResizeHost ...
func (w LoggedProvider) ResizeHost(id string, request abstract.SizingRequirements) (*abstract.Host, fail.Error) {
	defer w.prepare(w.trace("ResizeHost"))
	return w.InnerProvider.ResizeHost(id, request)
}

// CreateVolume ...
func (w LoggedProvider) CreateVolume(request abstract.VolumeRequest) (*abstract.Volume, fail.Error) {
	defer w.prepare(w.trace("CreateVolume"))
	return w.InnerProvider.CreateVolume(request)
}

// GetVolume ...
func (w LoggedProvider) GetVolume(id string) (*abstract.Volume, fail.Error) {
	defer w.prepare(w.trace("GetVolume"))
	return w.InnerProvider.GetVolume(id)
}

// ListVolumes ...
func (w LoggedProvider) ListVolumes() ([]abstract.Volume, fail.Error) {
	defer w.prepare(w.trace("ListVolumes"))
	return w.InnerProvider.ListVolumes()
}

// DeleteVolume ...
func (w LoggedProvider) DeleteVolume(id string) error {
	defer w.prepare(w.trace("DeleteVolume"))
	return w.InnerProvider.DeleteVolume(id)
}

// CreateVolumeAttachment ...
func (w LoggedProvider) CreateVolumeAttachment(request abstract.VolumeAttachmentRequest) (string, fail.Error) {
	defer w.prepare(w.trace("CreateVolumeAttachment"))
	return w.InnerProvider.CreateVolumeAttachment(request)
}

// GetVolumeAttachment ...
func (w LoggedProvider) GetVolumeAttachment(serverID, id string) (*abstract.VolumeAttachment, fail.Error) {
	defer w.prepare(w.trace("GetVolumeAttachment"))
	return w.InnerProvider.GetVolumeAttachment(serverID, id)
}

// ListVolumeAttachments ...
func (w LoggedProvider) ListVolumeAttachments(serverID string) ([]abstract.VolumeAttachment, fail.Error) {
	defer w.prepare(w.trace("ListVolumeAttachments"))
	return w.InnerProvider.ListVolumeAttachments(serverID)
}

// DeleteVolumeAttachment ...
func (w LoggedProvider) DeleteVolumeAttachment(serverID, id string) error {
	defer w.prepare(w.trace("DeleteVolumeAttachment"))
	return w.InnerProvider.DeleteVolumeAttachment(serverID, id)
}

// GetCapabilities returns the capabilities of the provider
func (w LoggedProvider) GetCapabilities() providers.Capabilities {
	defer w.prepare(w.trace("Getcapabilities"))
	return w.InnerProvider.GetCapabilities()
}
