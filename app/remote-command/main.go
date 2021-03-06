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
	"context"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"strings"
	"time"

	"github.com/opsmx/oes-birger/pkg/tunnel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

type environment []string

var (
	certFile   = flag.String("certFile", "tls.crt", "The file containing the certificate used to connect to the controller")
	keyFile    = flag.String("keyFile", "tls.key", "The file containing the certificate used to connect to the controller")
	caCertFile = flag.String("caCertFile", "ca.pem", "The file containing the CA certificate we will use to verify the controller's cert")
	host       = flag.String("host", "forwarder-controller:9001", "The hostname of the controller")
	cmd        = flag.String("cmd", "", "The remote command name to run")
	env        environment
)

func usage(message string) {
	if len(message) > 0 {
		fmt.Fprintf(os.Stderr, "ERROR: %s\n", message)
	}
	flag.Usage()
	os.Exit(-1)
}

func (i *environment) String() string {
	return fmt.Sprintf("%v", *i)
}

func (i *environment) Set(value string) error {
	if !strings.Contains(value, "=") {
		return fmt.Errorf("syntax: NAME=value")
	}
	*i = append(*i, value)
	return nil
}

func loadCert() []byte {
	cert, err := ioutil.ReadFile(*caCertFile)
	if err != nil {
		log.Fatalf("Unable to load certificate: %v", err)
	}
	return cert
}

func runCommand(client tunnel.CmdToolTunnelServiceClient, cmd string, env []string, args []string) {
	ctx := context.Background()
	stream, err := client.EventTunnel(ctx)
	if err != nil {
		log.Fatalf("%v.EventTunnel(_) = _, %v", client, err)
	}

	waitc := make(chan struct{})

	run := tunnel.CmdToolToControllerWrapper{
		Event: &tunnel.CmdToolToControllerWrapper_CommandRequest{
			CommandRequest: &tunnel.CmdToolCommandRequest{
				Name:        cmd,
				Arguments:   args,
				Environment: env,
			},
		},
	}
	err = stream.Send(&run)
	if err != nil {
		log.Fatalf("while sending to stream: %v", err)
	}
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
			case *tunnel.ControllerToCmdToolWrapper_CommandData:
				req := in.GetCommandData()
				if req.Channel == tunnel.ChannelDirection_STDOUT {
					fmt.Fprintf(os.Stdout, "%s", string(req.Body))
				} else {
					fmt.Fprintf(os.Stderr, "%s", string(req.Body))
				}
			case *tunnel.ControllerToCmdToolWrapper_CommandTermination:
				req := in.GetCommandTermination()
				if len(req.Message) > 0 {
					fmt.Fprintf(os.Stderr, "%s\n", req.Message)
				}
				os.Exit(int(req.ExitCode))
			case nil:
				continue
			default:
				log.Printf("Received unknown message: %T", x)
			}
		}
	}()
	<-waitc
	err = stream.CloseSend()
	log.Fatalf("While closing stream: %v", err)
}

func main() {
	flag.Var(&env, "env", "[repeatable] environment variable as NAME=value")
	flag.Parse()
	if len(*cmd) == 0 {
		usage("cmd must be specified")
	}
	if len(*host) == 0 {
		usage("host must be specified")
	}

	args := flag.Args()

	// load client cert/key, cacert
	clcert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		log.Fatalf("Unable to load certificate or key: %v", err)
	}
	caCertPool := x509.NewCertPool()
	srvcert := loadCert()
	if ok := caCertPool.AppendCertsFromPEM(srvcert); !ok {
		log.Fatalf("Unable to append certificate to pool: %v", err)
	}

	ta := credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{clcert},
		RootCAs:      caCertPool,
	})

	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(ta),
		grpc.WithBlock(),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, *host, opts...)
	if err != nil {
		log.Fatalf("Could not connect: %v", err)
	}
	defer conn.Close()

	client := tunnel.NewCmdToolTunnelServiceClient(conn)

	runCommand(client, *cmd, env, args)
}
