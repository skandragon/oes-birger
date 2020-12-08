package main

import (
	"crypto/tls"
	"crypto/x509"
	b64 "encoding/base64"
	"encoding/pem"
	"flag"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"golang.org/x/net/context"

	"google.golang.org/grpc"

	"github.com/skandragon/grpc-bidir/kubeconfig"
	"github.com/skandragon/grpc-bidir/tunnel"
)

var (
	host     = flag.String("host", tunnel.DefaultHostAndPort, "Server and port to connect to")
	rpcHost  = flag.String("rpcHost", "kubernetes.docker.internal:6443", "Host and port to connect to Kubernetes API")
	tickTime = flag.Int("tickTime", 30, "Time between sending Ping messages")
	identity = flag.String("identity", "", "The client ID to send to the server")
)

func makeHeaders(headers map[string][]string) []*tunnel.HttpHeader {
	ret := make([]*tunnel.HttpHeader, 0)
	for name, values := range headers {
		ret = append(ret, &tunnel.HttpHeader{Name: name, Values: values})
	}
	return ret
}
func runTunnel(config *serverConfig, client tunnel.TunnelServiceClient, ticker chan uint64, identity string) {
	ctx := context.Background()
	stream, err := client.EventTunnel(ctx)
	if err != nil {
		log.Fatalf("%v.EventTunnel(_) = _, %v", client, err)
	}

	// Sign in
	req := &tunnel.ASEventWrapper{
		Event: &tunnel.ASEventWrapper_SigninRequest{
			SigninRequest: &tunnel.SigninRequest{Identity: identity, StartTime: tunnel.Now()},
		},
	}
	log.Printf("Sending: %v", req)
	if err = stream.Send(req); err != nil {
		log.Fatalf("Unable to send a SigninRequest: %v", err)
	}

	// Handle periodic pings from the ticker.
	go func() {
		for {
			ts := <-ticker
			req := &tunnel.ASEventWrapper{
				Event: &tunnel.ASEventWrapper_PingRequest{
					PingRequest: &tunnel.PingRequest{Ts: ts},
				},
			}
			log.Printf("Sending %v", req)
			if err = stream.Send(req); err != nil {
				log.Fatalf("Unable to send a PingRequest: %v", err)
			}
		}
	}()

	waitc := make(chan struct{})
	go func() {
		for {
			in, err := stream.Recv()
			if err == io.EOF {
				// Server has closed the connection.
				close(waitc)
				return
			}
			if err != nil {
				log.Fatalf("Failed to receive a message: %T: %v", err, err)
			}
			switch x := in.Event.(type) {
			case *tunnel.SAEventWrapper_PingResponse:
				req := in.GetPingResponse()
				log.Printf("Received: PingResponse: %v", req)
			case *tunnel.SAEventWrapper_SigninResponse:
				req := in.GetSigninResponse()
				log.Printf("Succesfully signed in: %v", req)
			case *tunnel.SAEventWrapper_HttpRequest:
				req := in.GetHttpRequest()
				log.Printf("Processing HTTP request with id %s", req.Id)

				c := config.contexts[config.defaultContext]

				// TODO: A ServerCA is technically optional, but we might want to fail if it's not present...
				tlsConfig := &tls.Config{}
				if c.clientCert != nil {
					caCertPool := x509.NewCertPool()
					caCertPool.AddCert(c.serverCA)
					tlsConfig.Certificates = []tls.Certificate{*c.clientCert}
					tlsConfig.RootCAs = caCertPool
					tlsConfig.BuildNameToCertificate()
				}
				tlsConfig.InsecureSkipVerify = c.insecure
				tr := &http.Transport{
					MaxIdleConns:       10,
					IdleConnTimeout:    30 * time.Second,
					DisableCompression: true,
					TLSClientConfig:    tlsConfig,
				}
				client := &http.Client{
					Transport: tr,
				}

				httpRequest, _ := http.NewRequest(req.Method, c.serverURL+req.URI, nil)
				for _, header := range req.Headers {
					for _, value := range header.Values {
						httpRequest.Header.Add(header.Name, value)
					}
				}
				//log.Printf("Sending HTTP request: %v", httpRequest)
				get, err := client.Do(httpRequest)
				if err != nil {
					log.Printf("Failed to %s to %s: %v", req.Method, c.serverURL+req.URI, err)
					// TODO: respond with an error so the other side doesn't have to time out
					continue
				}
				body, _ := ioutil.ReadAll(get.Body)
				resp := &tunnel.ASEventWrapper{
					Event: &tunnel.ASEventWrapper_HttpResponse{
						HttpResponse: &tunnel.HttpResponse{
							Id:      req.Id,
							Target:  req.Target,
							Status:  int32(get.StatusCode),
							Body:    body,
							Headers: makeHeaders(get.Header),
						},
					},
				}
				log.Printf("Responding to HTTP request with id %s", req.Id)
				if err = stream.Send(resp); err != nil {
					log.Printf("Unable to respond over GRPC for request ID: %s: %v", req.Id, err)
					continue
				}
			case nil:
				// ignore for now
			default:
				log.Printf("Received unknown message: %T", x)
			}
		}
	}()
	<-waitc
	stream.CloseSend()
}

func runTicker(tickTime int, ticker chan uint64) {
	log.Printf("Starting ticker to send pings every %d seconds.", tickTime)
	go func() {
		for {
			time.Sleep(time.Duration(tickTime) * time.Second)
			ticker <- tunnel.Now()
		}
	}()

}

type serverContext struct {
	username   string
	serverURL  string
	serverCA   *x509.Certificate
	clientCert *tls.Certificate
	insecure   bool
}

type serverConfig struct {
	defaultContext string
	contexts       map[string]*serverContext
}

func makeServerConfig(kconfig *kubeconfig.KubeConfig) *serverConfig {

	contexts := make(map[string]*serverContext)

	names := kconfig.GetContextNames()
	for _, name := range names {
		user, cluster, err := kconfig.FindContext(name)
		if err != nil {
			log.Fatalf("Unable to retrieve cluster and user info for context %s: %v", name, err)
		}

		certData, err := b64.StdEncoding.DecodeString(user.User.ClientCertificateData)
		if err != nil {
			log.Fatalf("Error decoding user cert from base64 (%s): %v", user.Name, err)
		}
		keyData, err := b64.StdEncoding.DecodeString(user.User.ClientKeyData)
		if err != nil {
			log.Fatalf("Error decoding user key from base64 (%s): %v", user.Name, err)
		}

		clientKeypair, err := tls.X509KeyPair(certData, keyData)
		if err != nil {
			log.Fatalf("Error loading client cert/key: %v", err)
		}

		sa := &serverContext{
			username:   user.Name,
			clientCert: &clientKeypair,
			serverURL:  cluster.Cluster.Server,
			insecure:   cluster.Cluster.InsecureSkipTLSVerify,
		}

		if len(cluster.Cluster.CertificateAuthorityData) > 0 {
			serverCA, err := b64.StdEncoding.DecodeString(cluster.Cluster.CertificateAuthorityData)
			if err != nil {
				log.Fatalf("Error decoding server CA cert from base64 (%s): %v", cluster.Name, err)
			}
			pemBlock, _ := pem.Decode(serverCA)
			serverCert, err := x509.ParseCertificate(pemBlock.Bytes)
			if err != nil {
				log.Fatalf("Error parsing server certificate: %v", err)
			}
			// This may be needed if the certificate isn't a proper CA, but it seems to work in my testing
			// without it.
			//serverCert.BasicConstraintsValid = true
			//serverCert.IsCA = true
			//serverCert.KeyUsage = x509.KeyUsageCertSign

			sa.serverCA = serverCert
		}

		contexts[name] = sa
	}

	config := &serverConfig{
		defaultContext: kconfig.CurrentContext,
		contexts:       contexts,
	}
	return config
}

func main() {
	flag.Parse()
	if *identity == "" {
		log.Fatal("Must specify an -identity")
	}

	kconfig, err := kubeconfig.ReadKubeConfig()
	if err != nil {
		log.Fatalf("Unable to read kubeconfig: %v", err)
	}
	config := makeServerConfig(kconfig)
	log.Printf("Kubernetes context: %s", config.defaultContext)

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithInsecure())
	opts = append(opts, grpc.WithBlock())

	conn, err := grpc.Dial(*host, opts...)
	if err != nil {
		log.Fatalf("Could not connect: %v", err)
	}
	defer conn.Close()

	client := tunnel.NewTunnelServiceClient(conn)

	ticker := make(chan uint64)
	runTicker(*tickTime, ticker)

	log.Printf("Starting tunnel.")
	runTunnel(config, client, ticker, *identity)
	log.Printf("Done.")
}
