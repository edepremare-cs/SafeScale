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

package nfs

import (
	"context"

	"github.com/CS-SI/SafeScale/lib/system"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	"github.com/CS-SI/SafeScale/lib/utils/retry"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// Server getServer structure
type Server struct {
	SSHConfig *system.SSHConfig
}

// NewServer instantiates a new nfs.getServer struct
func NewServer(sshconfig *system.SSHConfig) (srv *Server, err fail.Error) {
	if sshconfig == nil {
		return nil, fail.InvalidParameterError("sshconfig", "cannot be nil")
	}

	server := Server{
		SSHConfig: sshconfig,
	}
	return &server, nil
}

// Install installs and configure NFS service on the remote host
func (s *Server) Install(ctx context.Context) fail.Error {
	if ctx == nil {
		return fail.InvalidParameterCannotBeNilError("ctx")
	}

	stdout, xerr := executeScript(ctx, *s.SSHConfig, "nfs_server_install.sh", map[string]interface{}{})
	if xerr != nil {
		xerr.Annotate("stdout", stdout)
		return fail.Wrap(xerr, "error executing script to install nfs server")
	}
	return nil
}

// AddShare configures a local path to be exported by NFS
func (s *Server) AddShare(
	ctx context.Context, path string,
	options string, /*securityModes []string, readOnly, rootSquash, secure, async, noHide, crossMount, subtreeCheck bool*/
) fail.Error {
	// FIXME: validate parameters

	share, xerr := NewShare(s, path, options)
	if xerr != nil {
		return fail.Wrap(xerr, "failed to create the share")
	}

	// acl := ExportACL{
	// 	IPAddress:          "*",
	// 	SecurityModes: []securityflavor.Enum{},
	// 	Options: ExportOptions{
	// 		ReadOnly:       readOnly,
	// 		NoRootSquash:   !rootSquash,
	// 		Secure:         secure,
	// 		Async:          async,
	// 		NoHide:         noHide,
	// 		CrossMount:     crossMount,
	// 		NoSubtreeCheck: !subtreeCheck,
	// 		SetFSID:        false,
	// 		AnonUID:        0,
	// 		AnonGID:        0,
	// 	},
	// }

	// for _, securityMode := range securityModes {
	// 	switch securityMode {
	// 	case "sys":
	// 		acl.SecurityModes = append(acl.SecurityModes, securityflavor.Sys)
	// 	case "krb5":
	// 		acl.SecurityModes = append(acl.SecurityModes, securityflavor.Krb5)
	// 	case "krb5i":
	// 		acl.SecurityModes = append(acl.SecurityModes, securityflavor.Krb5i)
	// 	case "krb5p":
	// 		acl.SecurityModes = append(acl.SecurityModes, securityflavor.Krb5p)
	// 	default:
	// 		return fmt.Errorf("cannot add the share, %s is not a valid security mode", securityMode)
	// 	}
	// }

	// share.AddACL(acl)

	return share.Add(ctx)
}

// RemoveShare stops export of a local mount point by NFS on the remote server
func (s *Server) RemoveShare(ctx context.Context, path string) fail.Error {
	data := map[string]interface{}{
		"Path": path,
	}

	stdout, xerr := executeScript(ctx, *s.SSHConfig, "nfs_server_path_unexport.sh", data)
	if xerr != nil {
		xerr.Annotate("stdout", stdout)
		return fail.Wrap(xerr, "error executing script to unexport a shared directory")
	}
	return nil
}

// MountBlockDevice mounts a block device in the remote system
func (s *Server) MountBlockDevice(
	ctx context.Context, deviceName, mountPoint, format string, doNotFormat bool,
) (string, fail.Error) {
	data := map[string]interface{}{
		"Device":      deviceName,
		"MountPoint":  mountPoint,
		"FileSystem":  format,
		"DoNotFormat": doNotFormat,
	}

	var stdout string
	// FIXME: Add a retry here only if we catch an executionerror of a connection error
	rerr := retry.WhileUnsuccessfulWithLimitedRetries(func() error {
		istdout, xerr := executeScript(ctx, *s.SSHConfig, "block_device_mount.sh", data)
		if xerr != nil {
			xerr.Annotate("stdout", istdout)
			return fail.Wrap(xerr, "error executing script to mount block device")
		}
		stdout = istdout
		return nil // we are done, break the retry
	}, temporal.GetMinDelay(), 0, 4) // 4 retries and that's it
	if rerr != nil {
		return "", fail.Wrap(rerr, "error executing script to mount block device")
	}

	return stdout, nil

}

// UnmountBlockDevice unmounts a local block device on the remote system
func (s *Server) UnmountBlockDevice(ctx context.Context, volumeUUID string) fail.Error {
	data := map[string]interface{}{
		"UUID": volumeUUID,
	}

	// FIXME: Add a retry here only if we catch an executionerror of a connection error
	rerr := retry.WhileUnsuccessfulWithLimitedRetries(func() error {
		stdout, xerr := executeScript(ctx, *s.SSHConfig, "block_device_unmount.sh", data)
		if xerr != nil {
			xerr.Annotate("stdout", stdout)
			return fail.Wrap(xerr, "error executing script to unmount block device")
		}
		return nil
	}, temporal.GetMinDelay(), 0, 4) // 4 retries and that's it
	if rerr != nil {
		return fail.Wrap(rerr, "error executing script to unmount block device")
	}

	return nil
}
