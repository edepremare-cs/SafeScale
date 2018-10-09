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

package install

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/viper"

	pb "github.com/CS-SI/SafeScale/broker"
	brokerclient "github.com/CS-SI/SafeScale/broker/client"
	"github.com/CS-SI/SafeScale/providers/metadata"

	"github.com/CS-SI/SafeScale/utils/provideruse"
	"github.com/CS-SI/SafeScale/utils/retry"

	"github.com/CS-SI/SafeScale/system"
)

const (
	componentScriptTemplateContent = `
rm -f /var/tmp/{{.reserved_Name}}.component.{{.reserved_Action}}_{{.reserved_Step}}.log
exec 1<&-
exec 2<&-
exec 1<>/var/tmp/{{.reserved_Name}}.component.{{.reserved_Action}}_{{.reserved_Step}}.log
exec 2>&1

{{ .reserved_BashLibrary }}

{{ .reserved_Content }}
`
)

var (
	componentScriptTemplate *template.Template
)

// parseTargets validates targets on the cluster from the component specification
// Without error, returns 'master target', 'private node target' and 'public node target'
func parseTargets(specs *viper.Viper) (string, string, string, error) {
	if !specs.IsSet("component.target.cluster") {
		return "", "", "", fmt.Errorf("component isn't suitable for a cluster")
	}

	master := strings.ToLower(strings.TrimSpace(specs.GetString("component.target.cluster.master")))
	switch master {
	case "":
		fallthrough
	case "false":
		fallthrough
	case "no":
		fallthrough
	case "none":
		fallthrough
	case "0":
		master = "0"
	case "any":
		fallthrough
	case "one":
		fallthrough
	case "1":
		master = "1"
	case "all":
		fallthrough
	case "*":
		master = "*"
	default:
		return "", "", "", fmt.Errorf("invalid value '%s' for field 'component.target.cluster.master'", master)
	}

	privnode := strings.ToLower(strings.TrimSpace(specs.GetString("component.target.cluster.node.private")))
	switch privnode {
	case "false":
		fallthrough
	case "no":
		fallthrough
	case "none":
		privnode = "0"
	case "any":
		fallthrough
	case "one":
		fallthrough
	case "1":
		privnode = "1"
	case "":
		fallthrough
	case "all":
		fallthrough
	case "*":
		privnode = "*"
	default:
		return "", "", "", fmt.Errorf("invalid value '%s' for field 'component.target.cluster.node.private'", privnode)
	}

	pubnode := strings.ToLower(strings.TrimSpace(specs.GetString("component.target.cluster.node.public")))
	switch pubnode {
	case "":
		fallthrough
	case "false":
		fallthrough
	case "no":
		fallthrough
	case "none":
		fallthrough
	case "0":
		pubnode = "0"
	case "any":
		fallthrough
	case "one":
		fallthrough
	case "1":
		pubnode = "1"
	case "all":
		fallthrough
	case "*":
		pubnode = "*"
	default:
		return "", "", "", fmt.Errorf("invalid value '%s' for field 'component.target.cluster.node.public'", pubnode)
	}

	if master == "0" && privnode == "0" && pubnode == "0" {
		return "", "", "", fmt.Errorf("invalid 'component.target.cluster': no target designated")
	}
	return master, privnode, pubnode, nil
}

// UploadStringToRemoteFile creates a file 'filename' on remote 'host' with the content 'content'
func UploadStringToRemoteFile(content string, host *pb.Host, filename string, owner, group, rights string) error {
	if content == "" {
		panic("content is empty!")
	}
	if host == nil {
		panic("host is nil!")
	}
	if filename == "" {
		panic("filename is empty!")
	}
	f, err := system.CreateTempFileFromString(content, 0600)
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %s", err.Error())
	}
	to := fmt.Sprintf("%s:%s", host.Name, filename)
	broker := brokerclient.New().Ssh
	retryErr := retry.WhileUnsuccessful(
		func() error {
			var retcode int
			retcode, _, _, err = broker.Copy(f.Name(), to, 15*time.Second, brokerclient.DefaultExecutionTimeout)
			if err != nil {
				return err
			}
			if retcode != 0 {
				// If retcode == 1 (general copy error), retry. It may be a temporary network incident
				if retcode == 1 {
					// File may exist on target, try to remote it
					_, _, _, err = broker.Run(host.Name, fmt.Sprintf("sudo rm -f %s", filename), 15*time.Second, brokerclient.DefaultExecutionTimeout)
					return fmt.Errorf("file may exist on remote with inappropriate access rights, deleted it and retrying")
				}
				if system.IsSCPRetryable(retcode) {
					err = fmt.Errorf("failed to copy temporary file to '%s' (retcode: %d=%s)", to, retcode, system.SCPErrorString(retcode))
				}
				return nil
			}
			return nil
		},
		1*time.Second,
		2*time.Minute,
	)
	os.Remove(f.Name())
	if retryErr != nil {
		switch retryErr.(type) {
		case retry.ErrTimeout:
			return fmt.Errorf("timeout trying to copy temporary file to '%s': %s", to, retryErr.Error())
		}
		return err
	}

	cmd := ""
	if owner != "" {
		cmd += "sudo chown " + owner + " " + filename
	}
	if group != "" {
		if cmd != "" {
			cmd += " && "
		}
		cmd += "sudo chgrp " + group + " " + filename
	}
	if rights != "" {
		if cmd != "" {
			cmd += " && "
		}
		cmd += "sudo chmod " + rights + " " + filename
	}
	retryErr = retry.WhileUnsuccessful(
		func() error {
			var retcode int
			retcode, _, _, err = broker.Run(host.Name, cmd, 15*time.Second, brokerclient.DefaultExecutionTimeout)
			if err != nil {
				return err
			}
			if retcode != 0 {
				err = fmt.Errorf("failed to change rights of file '%s' (retcode=%d)", to, retcode)
				return nil
			}
			return nil
		},
		2*time.Second,
		1*time.Minute,
	)
	if retryErr != nil {
		switch retryErr.(type) {
		case retry.ErrTimeout:
			return fmt.Errorf("timeout trying to change rights of file '%s' on host '%s': %s", filename, host.Name, err.Error())
		default:
			return fmt.Errorf("failed to change rights of file '%s' on host '%s': %s", filename, host.Name, retryErr.Error())
		}
	}
	return nil
}

// normalizeScript envelops the script with log redirection to /var/tmp/component.<name>.<action>.log
// and ensures BashLibrary are there
func normalizeScript(params map[string]interface{}) (string, error) {
	var err error

	if componentScriptTemplate == nil {
		// parse then execute the template
		componentScriptTemplate, err = template.New("normalize_script").Parse(componentScriptTemplateContent)
		if err != nil {
			return "", fmt.Errorf("error parsing bash template: %s", err.Error())
		}
	}

	// Configures BashLibrary template var
	bashLibrary, err := system.GetBashLibrary()
	if err != nil {
		return "", err
	}
	params["reserved_BashLibrary"] = bashLibrary

	dataBuffer := bytes.NewBufferString("")
	err = componentScriptTemplate.Execute(dataBuffer, params)
	if err != nil {
		return "", err
	}
	return dataBuffer.String(), nil
}

func replaceVariablesInString(text string, v Variables) (string, error) {
	tmpl, err := template.New("text").Parse(text)
	if err != nil {
		return "", fmt.Errorf("failed to parse: %s", err.Error())
	}
	dataBuffer := bytes.NewBufferString("")
	err = tmpl.Execute(dataBuffer, v)
	if err != nil {
		return "", fmt.Errorf("failed to replace variables: %s", err.Error())
	}
	return dataBuffer.String(), nil
}

func findConcernedHosts(list []string, c *Component) (string, error) {
	// No metadata yet for components, first host is designated concerned host
	if len(list) > 0 {
		return list[0], nil
	}
	return "", fmt.Errorf("no hosts")
	//for _, h := range list {
	//}
}

// installRequirements walks through requirements and installs them if needed
func installRequirements(c *Component, t Target, v Variables, s Settings) error {
	specs := c.Specs()
	yamlKey := "component.requirements.components"
	if specs.IsSet(yamlKey) {
		// if debug {
		//	hostInstance, clusterInstance, nodeInstance := determineContext(t)
		//	msgHead := fmt.Sprintf("Checking requirements of component '%s'", c.DisplayName())
		//	var msgTail string
		//	if hostInstance != nil {
		//		msgTail = fmt.Sprintf("on host '%s'", hostInstance.host.Name)
		//	}
		//	if nodeInstance != nil {
		//		msgTail = fmt.Sprintf("on cluster node '%s'", nodeInstance.host.Name)
		//	}
		//	if clusterInstance != nil {
		//		msgTail = fmt.Sprintf("on cluster '%s'", clusterInstance.cluster.GetName())
		//	}
		//	log.Printf("%s %s...\n", msgHead, msgTail)
		// }
		for _, requirement := range specs.GetStringSlice(yamlKey) {
			needed, err := NewComponent(requirement)
			if err != nil {
				return fmt.Errorf("failed to find required component '%s': %s", requirement, err.Error())
			}
			results, err := needed.Add(t, v, s)
			if err != nil {
				return fmt.Errorf("failed to install required component '%s': %s", requirement, err.Error())
			}
			if !results.Successful() {
				return fmt.Errorf("failed to install required component '%s':\n%s", requirement, results.AllErrorMessages())
			}
		}
	}
	return nil
}

// determineContext ...
func determineContext(t Target) (hT *HostTarget, cT *ClusterTarget, nT *NodeTarget) {
	hT = nil
	cT = nil
	nT = nil

	var ok bool

	hT, ok = t.(*HostTarget)
	if !ok {
		cT, ok = t.(*ClusterTarget)
		if !ok {
			nT, ok = t.(*NodeTarget)
		}
	}
	return
}

// Check if required parameters defined in specification file have been set in 'v'
func checkParameters(c *Component, v Variables) error {
	specs := c.Specs()
	if specs.IsSet("component.parameters") {
		params := specs.GetStringSlice("component.parameters")
		for _, k := range params {
			if _, ok := v[k]; !ok {
				return fmt.Errorf("missing value for parameter '%s'", k)
			}
		}
	}
	return nil
}

// setImplicitParameters configures parameters that are implicitely defined, based on context
func setImplicitParameters(t Target, v Variables) {
	hT, cT, nT := determineContext(t)
	if cT != nil {
		cluster := cT.cluster
		config := cluster.GetConfig()
		v["ClusterName"] = cluster.GetName()
		v["Complexity"] = strings.ToLower(config.Complexity.String())
		v["GatewayIP"] = config.GatewayIP
		v["MasterIDs"] = cluster.ListMasterIDs()
		v["MasterIPs"] = cluster.ListMasterIPs()
		if _, ok := v["Username"]; !ok {
			v["Username"] = "cladm"
			v["Password"] = config.AdminPassword
		}
		if _, ok := v["CIDR"]; !ok {
			svc, err := provideruse.GetProviderService()
			if err == nil {
				mn, err := metadata.LoadNetwork(svc, config.NetworkID)
				if err == nil {
					v["CIDR"] = mn.Get().CIDR
				}
			} else {
				fmt.Fprintf(os.Stderr, "failed to determine network CIDR")
			}
		}
	} else {
		var host *pb.Host
		if nT != nil {
			host = nT.HostTarget.host
		}
		if hT != nil {
			host = hT.host
		}
		v["Hostname"] = host.Name
		v["HostIP"] = host.PRIVATE_IP
		gw := gatewayFromHost(host)
		if gw != nil {
			v["GatewayIP"] = gw.PRIVATE_IP
		}
		if _, ok := v["Username"]; !ok {
			v["Username"] = "gpac"
		}
	}
}

func gatewayFromHost(host *pb.Host) *pb.Host {
	broker := brokerclient.New()
	gwID := host.GetGatewayID()
	// If host has no gateway, host is gateway
	if gwID == "" {
		return host
	}
	gw, err := broker.Host.Inspect(gwID, brokerclient.DefaultExecutionTimeout)
	if err != nil {
		return nil
	}
	return gw
}

// identifyHosts identifies hosts concerned based on 'targets' and returns a list of hosts
func identifyHosts(w *worker, targets stepTargets) ([]*pb.Host, error) {
	hostT, masterT, privnodeT, pubnodeT, err := targets.parse()
	if err != nil {
		return nil, err
	}

	hostsList := []*pb.Host{}

	if w.cluster == nil {
		if hostT != "" {
			hostsList = append(hostsList, w.host)
		}
	} else {
		switch masterT {
		case "1":
			host, err := w.AvailableMaster()
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, host)
		case "*":
			all, err := w.AllMasters()
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, all...)
		}

		switch privnodeT {
		case "1":
			host, err := w.AvailableNode(false)
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, host)
		case "*":
			hosts, err := w.AllNodes(false)
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, hosts...)
		}

		switch pubnodeT {
		case "1":
			host, err := w.AvailableNode(true)
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, host)
		case "*":
			hosts, err := w.AllNodes(true)
			if err != nil {
				return nil, err
			}
			hostsList = append(hostsList, hosts...)
		}
	}
	if len(hostsList) == 0 {
		return hostsList, fmt.Errorf("failed to identify hosts")
	}
	return hostsList, nil
}
