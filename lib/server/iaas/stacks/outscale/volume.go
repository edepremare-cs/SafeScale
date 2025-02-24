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

package outscale

import (
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/volumespeed"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/volumestate"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/retry"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// CreateVolume creates a block volume
func (s stack) CreateVolume(request abstract.VolumeRequest) (_ *abstract.Volume, ferr fail.Error) {
	nullAV := abstract.NewVolume()
	if s.IsNull() {
		return nullAV, fail.InvalidInstanceError()
	}
	if request.Name == "" {
		return nil, fail.InvalidParameterCannotBeEmptyStringError("volume name")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%v)", request).WithStopwatch().Entering()
	defer tracer.Exiting()

	v, xerr := s.InspectVolumeByName(request.Name)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			debug.IgnoreError(xerr)
			break // nolint
		default:
			return nullAV, xerr
		}
	} else if v != nil {
		return nullAV, abstract.ResourceDuplicateError("volume", request.Name)
	}

	IOPS := 0
	if request.Speed == volumespeed.Ssd {
		IOPS = request.Size * 300
		if IOPS > 13000 {
			IOPS = 13000
		}
	}
	resp, xerr := s.rpcCreateVolume(request.Name, int32(request.Size), int32(IOPS), s.fromAbstractVolumeSpeed(request.Speed))
	if xerr != nil {
		return nullAV, xerr
	}

	defer func() {
		if ferr != nil {
			if derr := s.rpcDeleteVolume(resp.VolumeId); derr != nil {
				_ = ferr.AddConsequence(fail.Wrap(derr, "cleaning up on failure, failed to delete Volume"))
			}
		}
	}()

	xerr = s.WaitForVolumeState(resp.VolumeId, volumestate.Available)
	if xerr != nil {
		return nullAV, xerr
	}

	volume := abstract.NewVolume()
	volume.ID = resp.VolumeId
	volume.Speed = s.toAbstractVolumeSpeed(resp.VolumeType)
	volume.Size = int(resp.Size)
	volume.State = volumestate.Available
	volume.Name = request.Name
	return volume, nil
}

func (s stack) toAbstractVolumeSpeed(t string) volumespeed.Enum {
	if s, ok := s.configurationOptions.VolumeSpeeds[t]; ok {
		return s
	}
	return volumespeed.Hdd
}

func (s stack) fromAbstractVolumeSpeed(speed volumespeed.Enum) string {
	for t, s := range s.configurationOptions.VolumeSpeeds {
		if s == speed {
			return t
		}
	}
	return s.fromAbstractVolumeSpeed(volumespeed.Hdd)
}

func toAbstractVolumeState(state string) volumestate.Enum {
	if state == "creating" {
		return volumestate.Creating
	}
	if state == "available" {
		return volumestate.Available
	}
	if state == "in-use" {
		return volumestate.Used
	}
	if state == "deleting" {
		return volumestate.Deleting
	}
	if state == "error" {
		return volumestate.Error
	}
	return volumestate.Unknown
}

// WaitForVolumeState wait for volume to be in the specified state
func (s stack) WaitForVolumeState(volumeID string, state volumestate.Enum) (xerr fail.Error) {
	if s.IsNull() {
		return fail.InvalidInstanceError()
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s)", volumeID).WithStopwatch().Entering()
	defer tracer.Exiting()

	return retry.WhileUnsuccessfulWithHardTimeout(
		func() error {
			vol, innerErr := s.InspectVolume(volumeID)
			if innerErr != nil {
				return innerErr
			}
			if vol.State != state {
				return fail.NewError("wrong state")
			}
			return nil
		},
		temporal.GetDefaultDelay(),
		temporal.GetHostTimeout(),
	)
}

// InspectVolume returns the volume identified by id
func (s stack) InspectVolume(id string) (av *abstract.Volume, xerr fail.Error) {
	nullAV := abstract.NewVolume()
	if s.IsNull() {
		return nullAV, fail.InvalidInstanceError()
	}
	if id == "" {
		return nullAV, fail.InvalidParameterCannotBeEmptyStringError("id")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s)", id).WithStopwatch().Entering()
	defer tracer.Exiting()

	resp, xerr := s.rpcReadVolumeByID(id)
	if xerr != nil {
		return nullAV, xerr
	}

	av = abstract.NewVolume()
	av.ID = resp.VolumeId
	av.Speed = s.toAbstractVolumeSpeed(resp.VolumeType)
	av.Size = int(resp.Size)
	av.State = toAbstractVolumeState(resp.State)
	av.Name = getResourceTag(resp.Tags, tagNameLabel, "")
	return av, nil
}

// InspectVolumeByName returns the volume with name name
func (s stack) InspectVolumeByName(name string) (av *abstract.Volume, xerr fail.Error) {
	nullAV := abstract.NewVolume()
	if s.IsNull() {
		return nullAV, fail.InvalidInstanceError()
	}
	if name == "" {
		return nullAV, fail.InvalidParameterCannotBeEmptyStringError("name")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "('%s')", name).WithStopwatch().Entering()
	defer tracer.Exiting()

	resp, xerr := s.rpcReadVolumeByName(name)
	if xerr != nil {
		return nullAV, xerr
	}

	av = abstract.NewVolume()
	av.ID = resp.VolumeId
	av.Speed = s.toAbstractVolumeSpeed(resp.VolumeType)
	av.Size = int(resp.Size)
	av.State = toAbstractVolumeState(resp.State)
	av.Name = name
	return av, nil
}

// ListVolumes list available volumes
func (s stack) ListVolumes() (_ []abstract.Volume, xerr fail.Error) {
	var emptySlice []abstract.Volume
	if s.IsNull() {
		return emptySlice, fail.InvalidInstanceError()
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale")).WithStopwatch().Entering()
	defer tracer.Exiting()

	resp, xerr := s.rpcReadVolumes(nil)
	if xerr != nil {
		return emptySlice, xerr
	}

	volumes := make([]abstract.Volume, 0, len(resp))
	for _, ov := range resp {
		volume := abstract.NewVolume()
		volume.ID = ov.VolumeId
		volume.Speed = s.toAbstractVolumeSpeed(ov.VolumeType)
		volume.Size = int(ov.Size)
		volume.State = toAbstractVolumeState(ov.State)
		volume.Name = getResourceTag(ov.Tags, "name", "")
		volume.Tags["CreationDate"] = getResourceTag(ov.Tags, "CreationDate", "")
		volume.Tags["ManagedBy"] = getResourceTag(ov.Tags, "ManagedBy", "")
		volume.Tags["DeclaredInBucket"] = getResourceTag(ov.Tags, "DeclaredInBucket", "")
		volumes = append(volumes, *volume)
	}
	return volumes, nil
}

// DeleteVolume deletes the volume identified by id
func (s stack) DeleteVolume(id string) (xerr fail.Error) {
	if s.IsNull() {
		return fail.InvalidInstanceError()
	}
	if id == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("id")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s)", id).WithStopwatch().Entering()
	defer tracer.Exiting()

	return s.rpcDeleteVolume(id)
}

func freeDevice(usedDevices []string, device string) bool {
	for _, usedDevice := range usedDevices {
		if device == usedDevice {
			return false
		}
	}
	return true
}

func (s stack) getFirstFreeDeviceName(serverID string) (string, fail.Error) {
	var usedDeviceNames []string
	atts, _ := s.ListVolumeAttachments(serverID)
	if atts == nil {
		if len(s.deviceNames) > 0 {
			return s.deviceNames[0], nil
		}
		return "", fail.InconsistentError("device names is empty")
	}
	for _, att := range atts {
		usedDeviceNames = append(usedDeviceNames, att.Device)
	}
	for _, deviceName := range s.deviceNames {
		if freeDevice(usedDeviceNames, deviceName) {
			return deviceName, nil
		}
	}
	return "", nil
}

// CreateVolumeAttachment attaches a volume to a host
func (s stack) CreateVolumeAttachment(request abstract.VolumeAttachmentRequest) (_ string, xerr fail.Error) {
	if s.IsNull() {
		return "", fail.InvalidInstanceError()
	}
	if request.HostID == "" {
		return "", fail.InvalidParameterCannotBeEmptyStringError("HostID")
	}
	if request.VolumeID == "" {
		return "", fail.InvalidParameterCannotBeEmptyStringError("VolumeID")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%v)", request).WithStopwatch().Entering()
	defer tracer.Exiting()

	firstDeviceName, xerr := s.getFirstFreeDeviceName(request.HostID)
	if xerr != nil {
		return "", xerr
	}

	if xerr := s.rpcLinkVolume(request.VolumeID, request.HostID, firstDeviceName); xerr != nil {
		return "", xerr
	}
	// No ID of attachment in outscale, volume can be attached to only one server; Volume Attachment ID is equal to VolumeID
	return request.VolumeID, nil
}

// InspectVolumeAttachment returns the volume attachment identified by volumeID
func (s stack) InspectVolumeAttachment(serverID, volumeID string) (_ *abstract.VolumeAttachment, xerr fail.Error) {
	nullVA := abstract.NewVolumeAttachment()
	if s.IsNull() {
		return nullVA, fail.InvalidInstanceError()
	}
	if serverID == "" {
		return nullVA, fail.InvalidParameterCannotBeEmptyStringError("serverID")
	}
	if volumeID == "" {
		return nullVA, fail.InvalidParameterCannotBeEmptyStringError("volumeID")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s, %s)", serverID, volumeID).WithStopwatch().Entering()
	defer tracer.Exiting()

	resp, xerr := s.rpcReadVolumeByID(volumeID)
	if xerr != nil {
		return nullVA, xerr
	}

	for _, lv := range resp.LinkedVolumes {
		if lv.VmId == serverID {
			return &abstract.VolumeAttachment{
				VolumeID:   volumeID,
				ServerID:   serverID,
				Device:     lv.DeviceName,
				Name:       "",
				MountPoint: "",
				Format:     "",
				ID:         volumeID,
			}, nil
		}
	}

	return nil, fail.NotFoundError("failed to find an attachment of Volume '%s' on Host '%s'", volumeID, serverID)
}

// ListVolumeAttachments lists available volume attachment
func (s stack) ListVolumeAttachments(serverID string) (_ []abstract.VolumeAttachment, xerr fail.Error) {
	emptySlice := make([]abstract.VolumeAttachment, 0)
	if s.IsNull() {
		return emptySlice, fail.InvalidInstanceError()
	}
	if serverID == "" {
		return emptySlice, fail.InvalidParameterCannotBeEmptyStringError("serverID")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s)", serverID).WithStopwatch().Entering()
	defer tracer.Exiting()

	volumes, err := s.ListVolumes()
	if err != nil {
		return nil, err
	}
	atts := make([]abstract.VolumeAttachment, 0, len(volumes))
	for _, v := range volumes {
		att, _ := s.InspectVolumeAttachment(serverID, v.ID)
		if att != nil {
			atts = append(atts, *att)
		}
	}
	return atts, nil
}

func (s stack) Migrate(operation string, params map[string]interface{}) (xerr fail.Error) {
	return nil
}

// DeleteVolumeAttachment deletes the volume attachment identified by id
func (s stack) DeleteVolumeAttachment(serverID, volumeID string) (xerr fail.Error) {
	if s.IsNull() {
		return fail.InvalidInstanceError()
	}
	if serverID == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("serverID")
	}
	if volumeID == "" {
		return fail.InvalidParameterCannotBeEmptyStringError("volumeID")
	}

	tracer := debug.NewTracer(nil, tracing.ShouldTrace("stacks.outscale"), "(%s, %s)", serverID, volumeID).WithStopwatch().Entering()
	defer tracer.Exiting()

	xerr = s.rpcUnlinkVolume(volumeID)
	if xerr != nil {
		return xerr
	}
	return s.WaitForVolumeState(volumeID, volumestate.Available)
}
