package main

import (
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/opsmx/oes-birger/app/controller/agent"
	"github.com/opsmx/oes-birger/pkg/ca"
	"github.com/opsmx/oes-birger/pkg/tunnel"
)

func runHTTPSServer(serverCert tls.Certificate) {
	log.Printf("Running service HTTPS listener on port %d", config.ServicePort)

	certPool, err := authority.MakeCertPool()
	if err != nil {
		log.Fatalf("While making certpool: %v", err)
	}

	tlsConfig := &tls.Config{
		ClientCAs:    certPool,
		ClientAuth:   tls.VerifyClientCertIfGiven,
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", serviceAPIHandler)

	server := &http.Server{
		Addr:      fmt.Sprintf(":%d", config.ServicePort),
		TLSConfig: tlsConfig,
		Handler:   mux,
	}

	server.ListenAndServeTLS("", "")
}

func extractEndpointFromCert(r *http.Request) (agentIdentity string, endpointType string, endpointName string, validated bool) {
	if len(r.TLS.PeerCertificates) == 0 {
		return "", "", "", false
	}

	names, err := ca.GetCertificateNameFromCert(r.TLS.PeerCertificates[0])
	if err != nil {
		log.Printf("%v", err)
		return "", "", "", false
	}

	if names.Purpose != ca.CertificatePurposeService {
		return "", "", "", false
	}

	return names.Agent, names.Type, names.Name, true
}

func extractEndpointFromJWT(r *http.Request) (agentIdentity string, endpointType string, endpointName string, validated bool) {
	var authPassword string
	var ok bool
	if _, authPassword, ok = r.BasicAuth(); !ok {
		return "", "", "", false
	}

	endpointType, endpointName, agentIdentity, err := ValidateJWT(jwtKeyset, authPassword)
	if err != nil {
		log.Printf("%v", err)
		return "", "", "", false
	}

	return agentIdentity, endpointType, endpointName, true
}

func extractEndpoint(r *http.Request) (agentIdentity string, endpointType string, endpointName string, err error) {
	agentIdentity, endpointType, endpointName, found := extractEndpointFromCert(r)
	if found {
		return agentIdentity, endpointType, endpointName, nil
	}

	agentIdentity, endpointType, endpointName, found = extractEndpointFromJWT(r)
	if found {
		return agentIdentity, endpointType, endpointName, nil
	}

	return "", "", "", fmt.Errorf("no valid credentials or JWT found")
}

func serviceAPIHandler(w http.ResponseWriter, r *http.Request) {
	agentIdentity, endpointType, endpointName, err := extractEndpoint(r)
	if err != nil {
		w.Write(httpError(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	ep := agent.AgentSearch{
		Name:         agentIdentity,
		EndpointType: endpointType,
		EndpointName: endpointName,
	}
	runAPIHandler(ep, w, r)
}

func runAPIHandler(ep agent.AgentSearch, w http.ResponseWriter, r *http.Request) {
	apiRequestCounter.WithLabelValues(ep.Name).Inc()

	transactionID := ulidContext.Ulid()

	body, _ := ioutil.ReadAll(r.Body)
	req := &tunnel.HttpRequest{
		Id:      transactionID,
		Type:    ep.EndpointType,
		Name:    ep.EndpointName,
		Method:  r.Method,
		URI:     r.RequestURI,
		Headers: makeHeaders(r.Header),
		Body:    body,
	}
	message := &HTTPMessage{Out: make(chan *tunnel.AgentToControllerWrapper), Cmd: req}
	sessionID, found := agents.Send(ep, message)
	if !found {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	ep.Session = sessionID

	cleanClose := false
	notify := r.Context().Done()
	go func() {
		<-notify
		if !cleanClose {
			agents.Cancel(ep, transactionID)
		}
	}()

	seenHeader := false
	isChunked := false
	flusher := w.(http.Flusher)
	for {
		in, more := <-message.Out
		if !more {
			if !seenHeader {
				log.Printf("Request timed out sending to agent")
				w.WriteHeader(http.StatusBadGateway)
			}
			cleanClose = true
			return
		}

		switch x := in.Event.(type) {
		case *tunnel.AgentToControllerWrapper_HttpResponse:
			resp := in.GetHttpResponse()
			seenHeader = true
			isChunked = resp.ContentLength < 0
			for name := range w.Header() {
				r.Header.Del(name)
			}
			for _, header := range resp.Headers {
				for _, value := range header.Values {
					w.Header().Add(header.Name, value)
				}
			}
			w.WriteHeader(int(resp.Status))
			if resp.ContentLength == 0 {
				cleanClose = true
				return
			}
		case *tunnel.AgentToControllerWrapper_HttpChunkedResponse:
			resp := in.GetHttpChunkedResponse()
			if !seenHeader {
				log.Printf("Error: got ChunkedResponse before HttpResponse")
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			if len(resp.Body) == 0 {
				cleanClose = true
				return
			}
			w.Write(resp.Body)
			if isChunked {
				flusher.Flush()
			}
		case nil:
			// ignore for now
		default:
			log.Printf("Received unknown message: %T", x)
		}
	}
}