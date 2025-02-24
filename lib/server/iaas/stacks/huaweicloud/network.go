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

package huaweicloud

import (
	"fmt"
	"net"
	"net/http"
	"strings"

	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks/openstack"
	"github.com/CS-SI/SafeScale/lib/utils/debug/tracing"
	"github.com/davecgh/go-spew/spew"
	"github.com/pengux/check"
	"github.com/sirupsen/logrus"

	"github.com/gophercloud/gophercloud"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/extensions/layer3/routers"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/networks"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/ports"
	"github.com/gophercloud/gophercloud/openstack/networking/v2/subnets"
	"github.com/gophercloud/gophercloud/pagination"

	"github.com/CS-SI/SafeScale/lib/server/iaas/stacks"
	"github.com/CS-SI/SafeScale/lib/server/resources/abstract"
	"github.com/CS-SI/SafeScale/lib/server/resources/enums/ipversion"
	"github.com/CS-SI/SafeScale/lib/utils/debug"
	"github.com/CS-SI/SafeScale/lib/utils/fail"
	netretry "github.com/CS-SI/SafeScale/lib/utils/net"
	"github.com/CS-SI/SafeScale/lib/utils/retry"
	"github.com/CS-SI/SafeScale/lib/utils/retry/enums/verdict"
	"github.com/CS-SI/SafeScale/lib/utils/temporal"
)

// VPCRequest defines a request to create a VPC
type VPCRequest struct {
	Name string `json:"name"`
	CIDR string `json:"cidr"`
}

// VPC contains information about a VPC
type VPC struct {
	ID      string `json:"id"`
	Name    string `json:"name,omitempty"`
	CIDR    string `json:"cidr,omitempty"`
	Status  string `json:"status,omitempty"`
	Network *networks.Network
	Router  *routers.Router
}

type vpcCommonResult struct {
	gophercloud.Result
}

// Router represents a router
type Router struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
	// NetworkID is the Network ID which the router gateway is connected to.
	NetworkID string `json:"network_id,omitempty"`
}

// Extract is a function that accepts a result and extracts a Network/VPC from FlexibleEngine response.
func (r vpcCommonResult) Extract() (*VPC, error) {
	var s struct {
		VPC *VPC `json:"vpc"`
	}
	err := r.ExtractInto(&s)
	return s.VPC, err
}

type vpcCreateResult struct {
	vpcCommonResult
}
type vpcGetResult struct {
	vpcCommonResult
}
type vpcDeleteResult struct { // nolint
	gophercloud.ErrResult
}

// HasDefaultNetwork returns true if the stack as a default network set (coming from tenants file)
func (s stack) HasDefaultNetwork() (bool, fail.Error) {
	if s.IsNull() {
		return false, nil
	}
	return s.vpc != nil, nil
}

// GetDefaultNetwork returns the *abstract.Network corresponding to the default network
func (s stack) GetDefaultNetwork() (*abstract.Network, fail.Error) {
	if s.IsNull() {
		return abstract.NewNetwork(), fail.InvalidInstanceError()
	}
	if s.vpc == nil {
		return abstract.NewNetwork(), fail.NotFoundError("no default Network in stack")
	}
	return s.vpc, nil
}

// CreateNetwork creates a Network, which corresponds to a VPC in FlexibleEngine terminology
func (s stack) CreateNetwork(req abstract.NetworkRequest) (*abstract.Network, fail.Error) {
	nullAN := abstract.NewNetwork()
	if s.IsNull() {
		return nullAN, fail.InvalidInstanceError()
	}

	gcReq := VPCRequest{
		Name: req.Name,
		CIDR: req.CIDR,
	}
	b, err := gophercloud.BuildRequestBody(gcReq, "vpc")
	if err != nil {
		return nullAN, normalizeError(err)
	}

	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/vpcs" // FIXME: Hardcoded endpoint
	resp := vpcCreateResult{}
	opts := gophercloud.RequestOpts{
		JSONBody:     b,
		JSONResponse: &resp.Body,
		OkCodes:      []int{200, 201},
	}
	commRetryErr := stacks.RetryableRemoteCall(
		func() error {
			_, innerErr := s.Driver.Request("POST", url, &opts)
			return innerErr
		},
		normalizeError,
	)
	if commRetryErr != nil {
		return nullAN, fail.Wrap(commRetryErr, "query to create VPC failed")
	}
	vpc, err := resp.Extract()
	if err != nil {
		return nullAN, normalizeError(err)
	}

	an := abstract.NewNetwork()
	an.ID = vpc.ID
	an.Name = req.Name
	an.CIDR = req.CIDR
	an.DNSServers = req.DNSServers

	return an, nil
}

// ListRouters lists available routers
func (s stack) ListRouters() ([]Router, fail.Error) {
	var emptySlice []Router
	if s.IsNull() {
		return emptySlice, fail.InvalidInstanceError()
	}

	var ns []Router
	xerr := stacks.RetryableRemoteCall(
		func() error {
			return routers.List(s.NetworkClient, routers.ListOpts{}).EachPage(
				func(page pagination.Page) (bool, error) {
					list, err := routers.ExtractRouters(page)
					if err != nil {
						return false, err
					}
					for _, r := range list {
						an := Router{
							ID:        r.ID,
							Name:      r.Name,
							NetworkID: r.GatewayInfo.NetworkID,
						}
						ns = append(ns, an)
					}
					return true, nil
				},
			)
		},
		NormalizeError,
	)
	return ns, xerr
}

// findVPCBoundOpenstackNetwork finds the Openstack Network resource associated to Huaweicloud VPC
func (s stack) findOpenStackNetworkBoundToVPC(vpcName string) (*networks.Network, fail.Error) {
	var router *Router
	found := false
	routerList, xerr := s.ListRouters()
	if xerr != nil {
		return nil, fail.Wrap(xerr, "failed to list routers")
	}
	for _, r := range routerList {
		local := r
		if r.Name == vpcName {
			found = true
			router = &local
			break
		}
	}
	if !found || router == nil {
		return nil, fail.NotFoundError(nil, nil, "failed to find router associated to VPC '%s'", vpcName)
	}

	var network *networks.Network
	commRetryErr := stacks.RetryableRemoteCall(
		func() (innerErr error) {
			network, innerErr = networks.Get(s.NetworkClient, router.NetworkID).Extract()
			return innerErr
		},
		normalizeError,
	)
	if commRetryErr != nil {
		return nil, fail.Wrap(commRetryErr, "failed to get information of binded network")
	}
	return network, nil
}

// InspectNetwork returns the information about a VPC identified by 'id'
func (s stack) InspectNetwork(id string) (*abstract.Network, fail.Error) {
	nullAN := abstract.NewNetwork()
	if s.IsNull() {
		return nullAN, fail.InvalidInstanceError()
	}
	if id = strings.TrimSpace(id); id == "" {
		return nullAN, fail.InvalidParameterError("id", "cannot be empty string")
	}

	r := vpcGetResult{}
	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/vpcs/" + id // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		JSONResponse: &r.Body,
		OkCodes:      []int{200, 201},
	}
	var vpc *VPC
	commRetryErr := stacks.RetryableRemoteCall(
		func() (innerErr error) {
			if _, innerErr = s.Driver.Request("GET", url, &opts); innerErr == nil {
				vpc, innerErr = r.Extract()
			}
			return innerErr
		},
		normalizeError,
	)
	if commRetryErr != nil {
		switch commRetryErr.(type) { // nolint
		case *fail.ErrInvalidRequest: // In case of VPC, when id does not exist, huaweicloud returns InvalidRequest... which cannot be the case because we validated that id is not empty
			return nil, fail.NotFoundError("failed to find Network with id %s", id)
		}
		return nil, commRetryErr
	}

	return toAbstractNetwork(*vpc), nil
}

// toAbstractNetwork converts a VPC to an *abstract.Network
func toAbstractNetwork(vpc VPC) *abstract.Network {
	an := abstract.NewNetwork()
	an.ID = vpc.ID
	an.Name = vpc.Name
	an.CIDR = vpc.CIDR
	return an
}

// InspectNetworkByName returns the information about a Network/VPC identified by 'name'
func (s stack) InspectNetworkByName(name string) (an *abstract.Network, xerr fail.Error) {
	nullAN := abstract.NewNetwork()
	if s.IsNull() {
		return nullAN, fail.InvalidInstanceError()
	}
	if name = strings.TrimSpace(name); name == "" {
		return nil, fail.InvalidParameterError("name", "cannot be empty string")
	}

	nets, xerr := s.ListNetworks()
	if xerr != nil {
		return nil, xerr
	}

	an = nil
	for _, v := range nets {
		if v.Name == name {
			an = v
			break
		}
	}
	if an == nil {
		return nil, fail.NotFoundError("failed to find VPC named '%s'", name)
	}

	return an, nil
}

// ListNetworks lists all the Network/VPC created
func (s stack) ListNetworks() ([]*abstract.Network, fail.Error) {
	var emptySlice []*abstract.Network
	if s.IsNull() {
		return emptySlice, fail.InvalidInstanceError()
	}

	r := vpcCommonResult{}
	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/vpcs" // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		JSONResponse: &r.Body,
		OkCodes:      []int{200, 201},
	}
	xerr := stacks.RetryableRemoteCall(
		func() error {
			_, innerErr := s.Driver.Request("GET", url, &opts)
			return innerErr
		},
		normalizeError,
	)
	if xerr != nil {
		return emptySlice, xerr
	}

	var list []*abstract.Network
	if vpcs, ok := r.Body.(map[string]interface{})["vpcs"].([]interface{}); ok {
		for _, v := range vpcs {
			item, ok := v.(map[string]interface{})
			if !ok {
				logrus.Warnf("vpc should be a map[string]interface{}")
				continue
			}
			an := abstract.NewNetwork()
			an.Name, ok = item["name"].(string)
			if !ok {
				logrus.Warnf("name should NOT be empty")
				continue
			}
			an.ID, ok = item["id"].(string)
			if !ok {
				logrus.Warnf("id should NOT be empty")
				continue
			}
			an.CIDR, ok = item["cidr"].(string)
			if !ok {
				logrus.Warnf("cidr should NOT be empty")
				continue
			}
			// FIXME: Missing validation, all previous fields should be NOT empty
			list = append(list, an)
		}
	}
	return list, nil
}

// DeleteNetwork deletes a Network/VPC identified by 'id'
func (s stack) DeleteNetwork(id string) fail.Error {
	if s.IsNull() {
		return fail.InvalidInstanceError()
	}
	if id == "" {
		return fail.InvalidParameterError("id", "cannot be empty string")
	}

	r := vpcCommonResult{}
	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/vpcs/" + id // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		JSONResponse: &r.Body,
		OkCodes:      []int{200, 201, 204},
	}
	return stacks.RetryableRemoteCall(
		func() (innerErr error) {
			var r *http.Response
			r, innerErr = s.Driver.Request("DELETE", url, &opts)
			_ = r
			return innerErr
		},
		normalizeError,
	)
}

// CreateSubnet creates a network (ie a subnet in the network associated to VPC in FlexibleEngine
func (s stack) CreateSubnet(req abstract.SubnetRequest) (subnet *abstract.Subnet, xerr fail.Error) {
	nullAS := abstract.NewSubnet()
	if s.IsNull() {
		return nullAS, fail.InvalidInstanceError()
	}

	tracer := debug.NewTracer(nil, true, "(%s)", req.Name).WithStopwatch().Entering()
	defer tracer.Exiting()

	if _, xerr = s.InspectSubnetByName(req.NetworkID, req.Name); xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// continue
			debug.IgnoreError(xerr)
		default:
			return nullAS, xerr
		}
	} else {
		return nullAS, fail.DuplicateError("subnet '%s' already exists", req.Name)
	}

	if ok, xerr := validateNetworkName(req.NetworkID); !ok {
		return nullAS, fail.Wrap(xerr, "network name '%s' invalid", req.Name)
	}

	an, xerr := s.InspectNetwork(req.NetworkID)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			an, xerr = s.InspectNetworkByName(req.NetworkID)
			if xerr != nil {
				return nullAS, xerr
			}
		default:
			return nullAS, xerr
		}
	}

	// Checks if CIDR is valid for huaweicloud
	xerr = s.validateCIDR(&req, an)
	if xerr != nil {
		return nullAS, xerr
	}

	// Creates the subnet
	resp, xerr := s.createSubnet(req)
	if xerr != nil {
		return nullAS, fail.Wrap(xerr, "error creating subnet '%s'", req.Name)
	}

	subnet = abstract.NewSubnet()
	subnet.ID = resp.ID
	subnet.Name = resp.Name
	subnet.CIDR = resp.CIDR
	subnet.IPVersion = fromIntIPVersion(resp.IPVersion)
	subnet.Network = an.ID

	return subnet, nil
}

func (s stack) validateCIDR(req *abstract.SubnetRequest, network *abstract.Network) fail.Error {
	_, networkDesc, _ := net.ParseCIDR(network.CIDR)
	_, subnetDesc, err := net.ParseCIDR(req.CIDR)
	if err != nil {
		return fail.Wrap(err, "failed to validate CIDR '%s' for Subnet '%s'", req.CIDR, req.Name)
	}
	if networkDesc.IP.Equal(subnetDesc.IP) && networkDesc.Mask.String() == subnetDesc.Mask.String() {
		return fail.InvalidRequestError("cannot create Subnet with CIDR '%s': equal to Network one", req.CIDR)
	}
	return nil
}

// validateNetworkName validates the name of a Network based on known FlexibleEngine requirements
func validateNetworkName(name string) (bool, fail.Error) {
	type checker struct{ Name string }
	s := check.Struct{
		"Name": check.Composite{
			check.NonEmpty{},
			check.Regex{Constraint: `^[a-zA-Z0-9_-]+$`},
			check.MaxChar{Constraint: 64},
		},
	}

	c := checker{Name: name}
	e := s.Validate(c)
	if e.HasErrors() {
		errors, _ := e.GetErrorsByKey("Name")
		var errs []string
		for _, msg := range errors {
			errs = append(errs, msg.Error())
		}
		return false, fail.NewError(strings.Join(errs, "; "))
	}
	return true, nil
}

// InspectSubnetByName ...
func (s stack) InspectSubnetByName(networkRef, name string) (*abstract.Subnet, fail.Error) {
	nullAS := abstract.NewSubnet()
	if s.IsNull() {
		return nullAS, fail.InvalidInstanceError()
	}
	if name = strings.TrimSpace(name); name == "" {
		return nullAS, fail.InvalidParameterError("name", "cannot be empty string")
	}

	// Gophercloud doesn't propose the way to get a host by name, but OpenStack knows how to do it...
	r := networks.GetResult{}
	xerr := stacks.RetryableRemoteCall(
		func() error {
			_, r.Err = s.NetworkClient.Get(s.NetworkClient.ServiceURL("subnets?name="+name), &r.Body, &gophercloud.RequestOpts{
				OkCodes: []int{200, 203},
			})
			return r.Err
		},
		normalizeError,
	)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrForbidden:
			return nullAS, abstract.ResourceForbiddenError("network", name)
		default:
			return nullAS, xerr
		}
	}

	subnetworks, found := r.Body.(map[string]interface{})["subnets"].([]interface{})
	if found && len(subnetworks) > 0 {
		var (
			entry map[string]interface{}
			id    string
		)
		for _, s := range subnetworks {
			var ok bool
			entry, ok = s.(map[string]interface{})
			if !ok {
				logrus.Warnf("subnet should be a map[string]interface{}")
				continue
			}
			id, ok = entry["id"].(string)
			if !ok {
				logrus.Warnf("id should be a string")
				continue
			}
		}
		return s.inspectOpenstackSubnet(id)
	}
	return nullAS, abstract.ResourceNotFoundError("subnet", name)
}

// InspectSubnet returns the subnet identified by id
func (s stack) InspectSubnet(id string) (*abstract.Subnet, fail.Error) {
	nullAS := abstract.NewSubnet()
	if s.IsNull() {
		return nullAS, fail.InvalidInstanceError()
	}
	if id == "" {
		return nullAS, fail.InvalidParameterError("id", "cannot be empty string")
	}

	r := subnetGetResult{}
	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/subnets/" + id // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		JSONResponse: &r.Body,
		OkCodes:      []int{200, 201},
	}
	var resp *subnetEx
	xerr := stacks.RetryableRemoteCall(
		func() error {
			_, innerErr := s.Driver.Request("GET", url, &opts)
			r.Err = innerErr
			resp, innerErr = r.Extract()
			return innerErr
		},
		normalizeError,
	)
	if xerr != nil {
		return nullAS, xerr
	}

	as := abstract.NewSubnet()
	as.ID = resp.Subnet.ID
	as.Name = resp.Subnet.Name
	as.CIDR = resp.Subnet.CIDR
	as.Network = resp.VpcID
	as.IPVersion = fromIntIPVersion(resp.IPVersion)
	return as, nil
}

func (s stack) inspectOpenstackSubnet(id string) (*abstract.Subnet, fail.Error) {
	nullAS := abstract.NewSubnet()
	if s.IsNull() {
		return nullAS, fail.InvalidInstanceError()
	}
	if id == "" {
		return nullAS, fail.InvalidParameterError("id", "cannot be empty string")
	}

	defer debug.NewTracer(nil, tracing.ShouldTrace("stack.network"), "(%s)", id).WithStopwatch().Entering().Exiting()

	as := abstract.NewSubnet()
	var sn *subnets.Subnet
	xerr := stacks.RetryableRemoteCall(
		func() (innerErr error) {
			sn, innerErr = subnets.Get(s.NetworkClient, id).Extract()
			return innerErr
		},
		NormalizeError,
	)
	if xerr != nil {
		return nullAS, xerr
	}

	as.ID = sn.ID
	as.Name = sn.Name
	as.Network = sn.NetworkID
	as.IPVersion = openstack.ToAbstractIPVersion(sn.IPVersion)
	as.CIDR = sn.CIDR
	as.DNSServers = sn.DNSNameservers

	return as, nil
}

// ListSubnets lists networks
func (s stack) ListSubnets(networkRef string) ([]*abstract.Subnet, fail.Error) {
	var emptySlice []*abstract.Subnet
	if s.IsNull() {
		return emptySlice, fail.InvalidInstanceError()
	}

	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/subnets" // FIXME: Hardcoded endpoint
	if networkRef != "" {
		url += "?vpc_id=" + networkRef
	}

	pager := pagination.NewPager(s.NetworkClient, url, func(r pagination.PageResult) pagination.Page {
		return subnets.SubnetPage{LinkedPageBase: pagination.LinkedPageBase{PageResult: r}}
	})
	var subnetList []*abstract.Subnet
	commRetryErr := stacks.RetryableRemoteCall(
		func() error {
			innerErr := pager.EachPage(func(page pagination.Page) (bool, error) {
				list, err := subnets.ExtractSubnets(page)
				if err != nil {
					return false, normalizeError(err)
				}

				for _, v := range list {
					item := abstract.NewSubnet()
					item.ID = v.ID
					item.Name = v.Name
					item.CIDR = v.CIDR
					item.Network = networkRef
					item.IPVersion = ipversion.Enum(v.IPVersion)
					item.DNSServers = v.DNSNameservers
					subnetList = append(subnetList, item)
				}
				return true, nil
			})
			return innerErr
		},
		normalizeError,
	)
	if commRetryErr != nil {
		return nil, commRetryErr
	}
	return subnetList, nil
}

// DeleteSubnet consists to delete subnet in FlexibleEngine VPC
func (s stack) DeleteSubnet(id string) fail.Error {
	if s.IsNull() {
		return fail.InvalidInstanceError()
	}
	if id == "" {
		return fail.InvalidParameterError("id", "cannot be empty string")
	}

	as, xerr := s.InspectSubnet(id)
	if xerr != nil {
		switch xerr.(type) {
		case *fail.ErrNotFound:
			// If subnet is not found, considered as a success
			debug.IgnoreError(xerr)
			return nil
		default:
			return xerr
		}
	}

	url := s.NetworkClient.Endpoint + "v1/" + s.authOpts.ProjectID + "/vpcs/" + as.Network + "/subnets/" + id // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		OkCodes: []int{204},
	}

	// FlexibleEngine has the curious behavior to be able to tell us all Hosts are deleted, but
	// cannot delete the subnet because there is still at least one host...
	// So we retry subnet deletion until all hosts are really deleted and subnet can be deleted
	return retry.Action(
		func() error {
			return stacks.RetryableRemoteCall(
				func() error {
					_, innerErr := s.Driver.Request("DELETE", url, &opts)
					return innerErr
				},
				normalizeError,
			)
		},
		retry.PrevailDone(retry.Unsuccessful(), retry.Timeout(temporal.GetHostCleanupTimeout())),
		retry.Constant(temporal.GetDefaultDelay()),
		nil,
		nil,
		func(t retry.Try, verdict verdict.Enum) {
			if t.Err != nil {
				switch t.Err.Error() {
				case "409":
					logrus.Debugf("Subnet still owns host(s), retrying in %s...", temporal.GetDefaultDelay())
				default:
					logrus.Warnf("unexpected error: %s", spew.Sdump(t.Err))
					logrus.Debugf("error submitting Subnet deletion (status=%s), retrying in %s...", t.Err.Error(), temporal.GetDefaultDelay())
				}
			}
		},
	)
}

type subnetRequest struct {
	Name             string   `json:"name"`
	CIDR             string   `json:"cidr"`
	GatewayIP        string   `json:"gateway_ip"`
	DHCPEnable       *bool    `json:"dhcp_enable,omitempty"`
	PrimaryDNS       string   `json:"primary_dns,omitempty"`
	SecondaryDNS     string   `json:"secondary_dns,omitempty"`
	DNSList          []string `json:"dnsList,omitempty"`
	AvailabilityZone string   `json:"availability_zone,omitempty"`
	VPCID            string   `json:"vpc_id"`
}

type subnetCommonResult struct {
	gophercloud.Result
}

type subnetEx struct {
	subnets.Subnet
	Status string `json:"status"`
	VpcID  string `json:"vpc_id"`
}

// Extract is a function that accepts a result and extracts a Subnet from FlexibleEngine response.
func (r subnetCommonResult) Extract() (*subnetEx, error) {
	var s struct {
		//		Subnet *subnets.Subnet `json:"subnet"`
		Subnet *subnetEx `json:"subnet"`
	}
	err := r.ExtractInto(&s)
	return s.Subnet, err
}

type subnetCreateResult struct {
	subnetCommonResult
}
type subnetGetResult struct {
	subnetCommonResult
}
type subnetDeleteResult struct { // nolint
	gophercloud.ErrResult
}

// createSubnet creates a subnet using native FlexibleEngine API
func (s stack) createSubnet(req abstract.SubnetRequest) (*subnets.Subnet, fail.Error) {
	network, _ /*networkDesc*/, _ := net.ParseCIDR(req.CIDR)

	// Calculate IP address for gateway
	n := netretry.IPv4ToUInt32(network)
	gw := netretry.UInt32ToIPv4(n + 1)

	dnsList := s.cfgOpts.DNSList
	if len(dnsList) == 0 {
		dnsList = []string{"1.1.1.1"}
	}
	var (
		primaryDNS   string
		secondaryDNS string
	)
	if len(dnsList) >= 1 {
		primaryDNS = dnsList[0]
	}
	if len(dnsList) >= 2 {
		secondaryDNS = dnsList[1]
	}
	bYes := true
	request := subnetRequest{
		Name:         req.Name,
		CIDR:         req.CIDR,
		VPCID:        req.NetworkID,
		DHCPEnable:   &bYes,
		GatewayIP:    gw.String(),
		PrimaryDNS:   primaryDNS,
		SecondaryDNS: secondaryDNS,
		DNSList:      dnsList,
	}
	b, err := gophercloud.BuildRequestBody(request, "subnet")
	if err != nil {
		return nil, normalizeError(err)
	}

	respCreate := subnetCreateResult{}
	url := fmt.Sprintf("%sv1/%s/subnets", s.NetworkClient.Endpoint, s.authOpts.ProjectID) // FIXME: Hardcoded endpoint
	opts := gophercloud.RequestOpts{
		JSONBody:     b,
		JSONResponse: &respCreate.Body,
		OkCodes:      []int{200, 201},
	}
	commRetryErr := stacks.RetryableRemoteCall(
		func() error {
			_, innerErr := s.Driver.Request("POST", url, &opts)
			return innerErr
		},
		normalizeError,
	)
	if commRetryErr != nil {
		return nil, commRetryErr
	}

	subnet, err := respCreate.Extract()
	if err != nil {
		return nil, normalizeError(err)
	}

	// Subnet creation started, need to wait the subnet to reach the status ACTIVE
	respGet := subnetGetResult{}
	opts.JSONResponse = &respGet.Body
	opts.JSONBody = nil

	retryErr := retry.WhileUnsuccessfulWithNotify(
		func() error {
			innerXErr := stacks.RetryableRemoteCall(
				func() error {
					_, innerErr := s.Driver.Request("GET", fmt.Sprintf("%s/%s", url, subnet.ID), &opts)
					return innerErr
				},
				normalizeError,
			)
			if innerXErr == nil {
				subnet, err = respGet.Extract()
				if err == nil && subnet.Status == "ACTIVE" {
					return nil
				}
			}
			return normalizeError(err)
		},
		temporal.GetMinDelay(),
		temporal.GetContextTimeout(),
		func(try retry.Try, v verdict.Enum) {
			if v != verdict.Done {
				logrus.Debugf("Network '%s' is not in 'ACTIVE' state, retrying...", req.Name)
			}
		},
	)
	if retryErr != nil {
		switch retryErr.(type) {
		case *retry.ErrStopRetry: // here it should never happen
			return nil, fail.Wrap(fail.Cause(retryErr), "stopping retries")
		case *retry.ErrTimeout:
			return nil, fail.Wrap(fail.Cause(retryErr), "timeout")
		default:
			return nil, retryErr
		}
	}

	return &subnet.Subnet, nil
}

func fromIntIPVersion(v int) ipversion.Enum {
	if v == 6 {
		return ipversion.IPv6
	}
	return ipversion.IPv4
}

// CreateVIP creates a private virtual IP
// If public is set to true,
func (s stack) CreateVIP(networkID, subnetID, name string, sgs []string) (*abstract.VirtualIP, fail.Error) {
	nullAVIP := abstract.NewVirtualIP()
	if s.IsNull() {
		return nullAVIP, fail.InvalidInstanceError()
	}
	if subnetID == "" {
		return nullAVIP, fail.InvalidParameterError("subnetID", "cannot be empty string")
	}
	if name == "" {
		return nullAVIP, fail.InvalidParameterError("name", "cannot be empty string")
	}

	// It seems FlexibleEngine encapsulates openstack subnet inside an openstack network; SubnetID is, in openstack context, a network ID.
	// So, we need to recover the real openstack network and subnet IDs for this call to succeed
	as, xerr := s.InspectSubnet(subnetID)
	if xerr != nil {
		return nullAVIP, xerr
	}

	openstackAS, xerr := s.InspectSubnetByName(networkID, as.Name)
	if xerr != nil {
		return nullAVIP, xerr
	}

	asu := true
	options := ports.CreateOpts{
		NetworkID:      openstackAS.Network,
		AdminStateUp:   &asu,
		Name:           name,
		SecurityGroups: &sgs,
		FixedIPs:       []ports.IP{{SubnetID: openstackAS.ID}},
	}
	port, err := ports.Create(s.NetworkClient, options).Extract()
	if err != nil {
		return nullAVIP, fail.ConvertError(err)
	}
	vip := abstract.VirtualIP{
		ID:        port.ID,
		Name:      name,
		NetworkID: networkID,
		SubnetID:  subnetID,
		PrivateIP: port.FixedIPs[0].IPAddress,
	}
	return &vip, nil
}
