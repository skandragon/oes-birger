package main

/*
 * Copyright 2021 OpsMx, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License")
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import (
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/OpsMx/go-app-base/version"
	"github.com/go-resty/resty/v2"
	"github.com/opsmx/oes-birger/internal/fwdapi"
)

var (
	action        = flag.String("action", "", "action, one of: kubectl, agent-manifest, service, or control")
	agentIdentity = flag.String("agent", "", "agent name")
	caCertFile    = flag.String("caCertFile", "ca-cert.pem", "The file containing the CA certificate we will use to verify the controller's cert")
	certFile      = flag.String("certFile", "control-cert.pem", "The file containing the certificate used to connect to the controller")
	endpointName  = flag.String("name", "", "Item name")
	endpointType  = flag.String("type", "", "endpoint type")
	keyFile       = flag.String("keyFile", "control-key.pem", "The file containing the certificate used to connect to the controller")
	outputFile    = flag.String("output-file", "", "The filename to write the full URL for this service.  Only valid for 'service' actions.")
	showversion   = flag.Bool("version", false, "show the version and exit")
	url           = flag.String("url", "https://forwarder-controller:9003", "The URL of the controller's control endpoint")
)

func usage(message string) {
	if len(message) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", message)
	}
	flag.Usage()
	fmt.Fprintf(os.Stderr, "\n")
	fmt.Fprintf(os.Stderr, "  'agent-manifest' requires: agent.\n")
	fmt.Fprintf(os.Stderr, "  'control' requires no other options.\n")
	fmt.Fprintf(os.Stderr, "  'kubectl' requires: agent, endpointName.\n")
	fmt.Fprintf(os.Stderr, "  'service' requires: agent, endpointType, endpointName.\n")
	os.Exit(-1)
}

func makeClient() *resty.Client {
	client := resty.New()
	client.SetRootCertificate(*caCertFile)
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Panicf("%v", err)
	}
	client.SetCertificates(cert)
	return client
}

func getKubeconfigCreds() {
	request := fwdapi.KubeConfigRequest{
		AgentName: *agentIdentity,
		Name:      *endpointName,
	}
	client := makeClient()
	resp, err := client.R().
		EnableTrace().
		SetBody(request).
		Post(fmt.Sprintf("%s%s", *url, fwdapi.KubeconfigEndpoint))
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	if resp.StatusCode() != 200 {
		log.Fatalf("Request failed: %s", resp.Status())
	}
	fmt.Printf("%s\n", string(resp.Body()))
}

func getAgentManifest() {
	request := fwdapi.ManifestRequest{
		AgentName: *agentIdentity,
	}
	client := makeClient()
	resp, err := client.R().
		EnableTrace().
		SetBody(request).
		Post(fmt.Sprintf("%s%s", *url, fwdapi.ManifestEndpoint))
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	if resp.StatusCode() != 200 {
		log.Fatalf("Request failed: %s", resp.Status())
	}
	fmt.Printf("%s\n", string(resp.Body()))
}

func check(err error) {
	if err != nil {
		log.Fatalf("fatal: %v", err)
	}
}

func getService() {
	request := fwdapi.ServiceCredentialRequest{
		AgentName: *agentIdentity,
		Type:      *endpointType,
		Name:      *endpointName,
	}
	client := makeClient()
	resp, err := client.R().
		EnableTrace().
		SetBody(request).
		Post(fmt.Sprintf("%s%s", *url, fwdapi.ServiceEndpoint))
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	if resp.StatusCode() != 200 {
		log.Fatalf("Request failed: %s", resp.Status())
	}
	if *outputFile == "" {
		fmt.Printf("%s\n", string(resp.Body()))
	} else {
		var f io.WriteCloser
		f, err = os.Create(*outputFile)
		check(err)
		defer f.Close()
		check(writeToFile(f, resp.Body()))
	}
}

func writeToFile(f io.Writer, sj []byte) error {
	var config fwdapi.ServiceCredentialResponse
	if err := json.Unmarshal(sj, &config); err != nil {
		return err
	}
	if config.CredentialType != "basic" {
		return fmt.Errorf("unsupported credential URL type %s, expected 'basic'", config.CredentialType)
	}
	creds, ok := config.Credential.(map[string]interface{})
	if !ok {
		return fmt.Errorf("Cannot cast to basic credential")
	}
	items := strings.SplitN(config.URL, ":", 2)
	username := creds["username"].(string)
	password := creds["password"].(string)
	target := items[0] + "://" + username + ":" + password + "@" + items[1][2:]
	fmt.Fprintln(f, target)
	return nil
}

func getControl() {
	request := fwdapi.ControlCredentialsRequest{
		Name: *endpointName,
	}
	client := makeClient()
	resp, err := client.R().
		EnableTrace().
		SetBody(request).
		Post(fmt.Sprintf("%s%s", *url, fwdapi.ControlEndpoint))
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	if resp.StatusCode() != 200 {
		log.Fatalf("Request failed: %s", resp.Status())
	}
	fmt.Printf("%s\n", string(resp.Body()))
}

func getStatistics() {
	client := makeClient()
	resp, err := client.R().
		EnableTrace().
		Get(fmt.Sprintf("%s%s", *url, fwdapi.StatisticsEndpoint))
	if err != nil {
		fmt.Printf("%v\n", err)
	}
	if resp.StatusCode() != 200 {
		log.Fatalf("Request failed: %s", resp.Status())
	}
	fmt.Printf("%s\n", string(resp.Body()))
}

func insist(s *string, name string, expected bool) {
	if expected && (s == nil || *s == "") {
		usage(fmt.Sprintf("%s: required", name))
	}
	if !expected && (s != nil && *s != "") {
		log.Panicf("%s: not allowed for this action", name)
	}
}

func main() {
	log.Printf("%s", version.VersionString())
	flag.Parse()
	if *showversion {
		os.Exit(0)
	}

	switch *action {
	case "kubectl":
		insist(agentIdentity, "agent", true)
		insist(endpointName, "name", true)
		insist(endpointType, "type", false)
		getKubeconfigCreds()
	case "agent-manifest":
		insist(agentIdentity, "agent", true)
		insist(endpointName, "name", false)
		insist(endpointType, "type", false)
		getAgentManifest()
	case "service":
		insist(agentIdentity, "agent", true)
		insist(endpointName, "name", true)
		insist(endpointType, "type", true)
		getService()
	case "control":
		insist(agentIdentity, "agent", false)
		insist(endpointName, "name", true)
		insist(endpointType, "type", false)
		getControl()
	case "statistics":
		//insist(agentIdentity, "agent", false)
		insist(endpointName, "name", false)
		insist(endpointType, "type", false)
		getStatistics()
	default:
		usage(fmt.Sprintf("Unknown action: %s", *action))
	}
}
