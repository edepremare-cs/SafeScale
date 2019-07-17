/*
 * Copyright 2018-2019, CS Systemes d'Information, http://www.c-s.fr
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
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	log "github.com/sirupsen/logrus"

	pb "github.com/CS-SI/SafeScale/lib"
	safescale "github.com/CS-SI/SafeScale/lib/client"
	"github.com/CS-SI/SafeScale/lib/utils"
	"github.com/CS-SI/SafeScale/lib/utils/concurrency"
)

const (
	curlGet   = "curl -kSsl -X GET --url https://localhost:8444/%s -H \"Content-Type:application/json\" -w \"\\n%%{http_code}\""
	curlPost  = "curl -kSsl -X POST --url https://localhost:8444/%s -H \"Content-Type:application/json\" -w \"\\n%%{http_code}\" -d @- <<'EOF'\n%s\nEOF\n"
	curlPut   = "curl -kSsl -X PUT --url https://localhost:8444/%s -H \"Content-Type:application/json\" -w \"\\n%%{http_code}\" -d @- <<'EOF'\n%s\nEOF\n"
	curlPatch = "curl -kSsl -X PATCH --url https://localhost:8444/%s -H \"Content-Type:application/json\" -w \"\\n%%{http_code}\" -d @- <<'EOF'\n%s\nEOF\n"
)

var kongProxyCheckedCache = utils.NewMapCache()

// KongController allows to control Kong, installed on a host
type KongController struct {
	host      *pb.Host
	safescale safescale.Client
}

// NewKongController ...
func NewKongController(host *pb.Host) (*KongController, error) {
	if host == nil {
		panic("host is nil!")
	}

	// Check if reverseproxy feature is installed on host
	rp, err := NewEmbeddedFeature(concurrency.VoidTask(), "kong4gateway")
	if err != nil {
		return nil, fmt.Errorf("failed to find a feature called 'kong4gateway'")
	}
	present := false
	if anon, ok := kongProxyCheckedCache.Get(host.Name); ok {
		present = anon.(bool)
	} else {
		setErr := kongProxyCheckedCache.SetBy(host.Name, func() (interface{}, error) {
			target := NewNodeTarget(host)
			if err != nil {
				return nil, err
			}
			results, err := rp.Check(target, Variables{}, Settings{})
			if err != nil {
				return nil, fmt.Errorf("failed to check if feature 'kong4gateway' is installed on gateway: %s", err.Error())
			}
			return results.Successful(), nil
		})
		if setErr != nil {
			return nil, setErr
		}
		present = true
	}
	if !present {
		return nil, fmt.Errorf("'kong4gateway' feature isn't installed on gateway")
	}

	return &KongController{
		host:      host,
		safescale: safescale.New(),
	}, nil
}

// Apply applies the rule to Kong proxy
// Currently, support rule types service, route and upstream
func (k *KongController) Apply(rule map[interface{}]interface{}, values *Variables) (Variables, error) {
	ruleName := rule["name"].(string)
	ruleType := rule["type"].(string)

	content, err := k.realizeRuleData(strings.Trim(rule["content"].(string), "\n"), *values)
	if err != nil {
		return nil, err
	}

	var sourceControl map[string]interface{}

	// Analyzes the rule...
	var url string
	switch ruleType {
	case "service":
		url = "services/"

		unjsoned := map[string]interface{}{}
		err = json.Unmarshal([]byte(content), &unjsoned)
		if err != nil {
			return nil, fmt.Errorf("syntax error in rule '%s': %s", ruleName, err.Error())
		}
		if _, ok := unjsoned["source-control"]; ok {
			sourceControl = unjsoned["source-control"].(map[string]interface{})
			delete(unjsoned, "source-control")
		}
		unjsoned["name"] = ruleName
		jsoned, _ := json.Marshal(&unjsoned)
		content = string(jsoned)

		response, _, propagated, err := k.put(ruleName, url, content, values, true)
		if err != nil {
			log.Debugf("")
			return nil, fmt.Errorf("failed to apply proxy rule '%s': %s", ruleName, err.Error())
		}
		return propagated, k.addSourceControl(ruleName, url, ruleType, response["id"].(string), sourceControl, values)

	case "route":
		url = "routes/"

		unjsoned := map[string]interface{}{}
		err = json.Unmarshal([]byte(content), &unjsoned)
		if err != nil {
			return nil, fmt.Errorf("syntax error in rule '%s': %s", ruleName, err.Error())
		}
		if _, ok := unjsoned["source-control"]; ok {
			sourceControl = unjsoned["source-control"].(map[string]interface{})
			delete(unjsoned, "source-control")
		}
		unjsoned["name"] = ruleName // To name the route...
		unjsoned["protocols"] = []string{"https"}
		jsoned, _ := json.Marshal(&unjsoned)
		content = string(jsoned)
		response, _, propagated, err := k.put(ruleName, url, content, values, true)
		if err != nil {
			log.Debugf("")
			return nil, fmt.Errorf("failed to apply proxy rule '%s': %s", ruleName, err.Error())
		}
		return propagated, k.addSourceControl(ruleName, url, ruleType, response["id"].(string), sourceControl, values)

	case "upstream":
		url = "upstreams/"

		// Create upstream if it doesn't exist
		url += ruleName
		create := false
		_, _, err = k.get(ruleName, url)
		if err != nil {
			if _, ok := err.(utils.ErrNotFound); !ok {
				return nil, err
			}
			create = true
		}
		var propagated Variables
		if create {
			propagated, err = k.createUpstream(ruleName, values)
			if err != nil {
				return nil, err
			}
		}

		// Now ready to add target to upstream
		url += "/targets"
		_, _, _, err = k.post(ruleName, url, content, values, false)
		if err != nil {
			return nil, fmt.Errorf("failed to apply proxy rule '%s': %s", ruleName, err.Error())
		}
		return propagated, nil

	default:
		return nil, fmt.Errorf("syntax error in rule '%s': %s isn't a valid type", ruleName, ruleType)
	}
}

func (k *KongController) realizeRuleData(content string, v Variables) (string, error) {
	contentTmpl, err := template.New("proxy_rule").Parse(content)
	if err != nil {
		return "", fmt.Errorf("error preparing rule: %s", err.Error())
	}
	dataBuffer := bytes.NewBufferString("")
	err = contentTmpl.Execute(dataBuffer, v)
	if err != nil {
		return "", err
	}
	return dataBuffer.String(), nil
}

func (k *KongController) createUpstream(name string, v *Variables) (Variables, error) {
	upstreamData := map[string]string{"name": name}
	jsoned, _ := json.Marshal(&upstreamData)
	response, _, propagated, err := k.post(name, "upstreams/", string(jsoned), v, true)
	if response == nil && err != nil {
		return nil, err
	}
	if msg, ok := response["message"]; ok {
		return nil, fmt.Errorf("failed to create upstream: %s", msg)
	}
	return propagated, nil
}

func (k *KongController) addSourceControl(ruleName, url, resourceType, resourceID string, sourceControl map[string]interface{}, v *Variables) error {
	if sourceControl == nil {
		return nil
	}

	// Determine if source-control is already set
	ref := ""
	url += fmt.Sprintf("%s/plugins", resourceID)
	result, _, err := k.get(ruleName, url)
	if err != nil {
		if _, ok := err.(utils.ErrNotFound); !ok {
			return err
		}
	}
	if data, ok := result["data"].([]interface{}); ok && len(data) > 0 {
		for _, i := range data {
			plugin := i.(map[string]interface{})
			if plugin["name"] == "ip-restriction" {
				ref = plugin["id"].(string)
				break
			}
		}
	}

	// Build data to submit to kong
	data := map[string]interface{}{
		"name":       "ip-restriction",
		resourceType: map[string]interface{}{"id": resourceID},
		"config":     sourceControl,
	}
	jsoned, _ := json.Marshal(&data)

	// Create or patch plugin ip-restriction
	if ref == "" {
		_, _, _, err = k.post(ruleName, url, string(jsoned), v, false)
	} else {
		_, _, _, err = k.patch(ref, "plugins/", string(jsoned), v, false)
	}
	if err != nil {
		msg := fmt.Sprintf("failed to apply setting 'source-control' of proxy rule '%s': %s", ruleName, err.Error())
		log.Debugf(utils.Capitalize(msg))
		return fmt.Errorf(msg)
	}
	return nil
}

func (k *KongController) buildSourceControlContent(rules map[string]interface{}) string {
	data := map[string]interface{}{
		"config": rules,
	}
	data["name"] = "ip-restriction"
	jsoned, _ := json.Marshal(&data)
	return string(jsoned)
}

func (k *KongController) get(name, url string) (map[string]interface{}, string, error) {
	cmd := fmt.Sprintf(curlGet, url)
	retcode, stdout, _, err := safescale.New().Ssh.Run(k.host.Name, cmd, safescale.DefaultConnectionTimeout, safescale.DefaultExecutionTimeout)
	if err != nil {
		return nil, "", err
	}
	if retcode != 0 {
		return nil, "", fmt.Errorf("get '%s' failed: retcode=%d", name, retcode)
	}

	response, httpcode, err := k.parseResult(stdout)
	if err != nil {
		return nil, httpcode, err
	}
	return response, httpcode, nil
}

func (k *KongController) post(name, url, data string, v *Variables, propagate bool) (map[string]interface{}, string, Variables, error) {
	propagated := Variables{}
	cmd := fmt.Sprintf(curlPost, url, data)
	retcode, stdout, stderr, err := safescale.New().Ssh.Run(k.host.Name, cmd, safescale.DefaultConnectionTimeout, safescale.DefaultExecutionTimeout)
	if err != nil {
		return nil, "", nil, err
	}
	if retcode != 0 {
		log.Debugf("submit of rule '%s' failed: retcode=%d, stdout=>>%s<<, stderr=>>%s<<", name, retcode, stdout, stderr)
		return nil, "", nil, fmt.Errorf("submit of rule '%s' failed: retcode=%d", name, retcode)
	}
	response, httpcode, err := k.parseResult(stdout)
	if err != nil {
		return nil, httpcode, nil, err
	}
	if propagate {
		if id, ok := response["id"]; ok {
			propagated[name] = id.(string)
		}
	}
	return response, httpcode, propagated, nil
}

func (k *KongController) put(name, url, data string, v *Variables, propagate bool) (map[string]interface{}, string, Variables, error) {
	propagated := Variables{}
	cmd := fmt.Sprintf(curlPut, url+name, data)
	retcode, stdout, stderr, err := safescale.New().Ssh.Run(k.host.Name, cmd, safescale.DefaultConnectionTimeout, safescale.DefaultExecutionTimeout)
	if err != nil {
		return nil, "", nil, err
	}
	if retcode != 0 {
		log.Debugf("submit of rule '%s' failed: retcode=%d, stdout=>>%s<<, stderr=>>%s<<", name, retcode, stdout, stderr)
		return nil, "", nil, fmt.Errorf("submit of rule '%s' failed: retcode=%d", name, retcode)
	}

	response, httpcode, err := k.parseResult(stdout)
	if err != nil {
		return nil, httpcode, nil, err
	}
	if propagate {
		if id, ok := response["id"]; ok {
			propagated[name] = id.(string)
		}
	}
	return response, httpcode, propagated, nil
}

func (k *KongController) patch(name, url, data string, v *Variables, propagate bool) (map[string]interface{}, string, Variables, error) {
	cmd := fmt.Sprintf(curlPatch, url+name, data)
	retcode, stdout, stderr, err := safescale.New().Ssh.Run(k.host.Name, cmd, safescale.DefaultConnectionTimeout, safescale.DefaultExecutionTimeout)
	if err != nil {
		return nil, "", nil, err
	}
	if retcode != 0 {
		log.Debugf("update of rule '%s' failed: retcode=%d, stdout=>>%s<<, stderr=>>%s<<", name, retcode, stdout, stderr)
		return nil, "", nil, fmt.Errorf("update of rule '%s' failed: retcode=%d", name, retcode)
	}

	response, httpcode, err := k.parseResult(stdout)
	if err != nil {
		return nil, httpcode, nil, err
	}

	propagated := Variables{}
	if propagate {
		if id, ok := response["id"]; ok {
			propagated[name] = id.(string)
		}
	}
	return response, httpcode, propagated, nil
}

func (k *KongController) parseResult(result string) (map[string]interface{}, string, error) {
	output := strings.Split(result, "\n")
	httpcode := output[1]

	var response map[string]interface{}
	err := json.Unmarshal([]byte(output[0]), &response)
	if err != nil {
		return nil, "", err
	}

	switch httpcode {
	case "200":
		fallthrough
	case "201":
		return response, httpcode, nil
	case "404":
		if msg, ok := response["message"]; ok {
			return nil, httpcode, utils.NotFoundError(msg.(string))
		}
		return nil, output[1], utils.NotFoundError("")
	case "409":
		if msg, ok := response["message"]; ok {
			return nil, httpcode, utils.DuplicateError(msg.(string))
		}
		return nil, httpcode, utils.DuplicateError("")
	default:
		if msg, ok := response["message"]; ok {
			return response, httpcode, fmt.Errorf("post failed: HTTP error code=%s: %s", httpcode, msg.(string))
		}
		return response, httpcode, fmt.Errorf("post failed with HTTP error code '%s'", httpcode)
	}
}
