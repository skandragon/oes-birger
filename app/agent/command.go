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
	"fmt"
	"io"
	"log"
	"os/exec"
	"syscall"

	"github.com/opsmx/oes-birger/pkg/tunnel"
	"golang.org/x/net/context"
)

func outputSender(channel tunnel.ChannelDirection, c chan *outputMessage, in io.Reader) {
	buffer := make([]byte, 10240)
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			// we need to copy the underlying data, since we will re-use it.
			tmp := make([]byte, n)
			copy(tmp, buffer[:n])
			c <- &outputMessage{channel: channel, value: tmp, closed: false}
		}
		if err == io.EOF {
			c <- &outputMessage{channel: channel, value: emptyBytes, closed: true}
			return
		}
		if err != nil {
			log.Printf("Got %v in read", err)
			c <- &outputMessage{channel: channel, value: emptyBytes, closed: true}
			return
		}
	}
}

type outputMessage struct {
	channel tunnel.ChannelDirection
	value   []byte
	closed  bool
}

func makeCommandFailed(req *tunnel.CommandRequest, err error, message string) *tunnel.AgentToControllerWrapper {
	var msg string
	if err != nil {
		msg = fmt.Sprintf("%s: %v", message, err)
	} else {
		msg = message
	}
	return &tunnel.AgentToControllerWrapper{
		Event: &tunnel.AgentToControllerWrapper_CommandTermination{
			CommandTermination: &tunnel.CommandTermination{
				Id:       req.Id,
				ExitCode: 127,
				Message:  msg,
			},
		},
	}
}

func makeCommandTermination(req *tunnel.CommandRequest, exitstatus int) *tunnel.AgentToControllerWrapper {
	return &tunnel.AgentToControllerWrapper{
		Event: &tunnel.AgentToControllerWrapper_CommandTermination{
			CommandTermination: &tunnel.CommandTermination{
				Id:       req.Id,
				ExitCode: int32(exitstatus),
			},
		},
	}
}

func makeCommandData(req *tunnel.CommandRequest, channel tunnel.ChannelDirection, data []byte) *tunnel.AgentToControllerWrapper {
	return &tunnel.AgentToControllerWrapper{
		Event: &tunnel.AgentToControllerWrapper_CommandData{
			CommandData: &tunnel.CommandData{
				Id:      req.Id,
				Body:    data,
				Channel: channel,
			},
		},
	}
}

func makeCommandDataClosed(req *tunnel.CommandRequest, channel tunnel.ChannelDirection) *tunnel.AgentToControllerWrapper {
	return &tunnel.AgentToControllerWrapper{
		Event: &tunnel.AgentToControllerWrapper_CommandData{
			CommandData: &tunnel.CommandData{
				Id:      req.Id,
				Channel: channel,
				Closed:  true,
			},
		},
	}
}

func runCommand(dataflow chan *tunnel.AgentToControllerWrapper, req *tunnel.CommandRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	registerCancelFunction(req.Id, cancel)
	defer unregisterCancelFunction(req.Id)

	log.Printf("Got command request: %v", req)

	// aggregation channel, for stdout and stderr to be send through.
	agg := make(chan *outputMessage)

	cmd := exec.CommandContext(ctx, req.Name, req.Arguments...)
	cmd.Env = req.Environment
	cmd.SysProcAttr = &syscall.SysProcAttr{}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: 65534, Gid: 65534}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		dataflow <- makeCommandFailed(req, err, "StdoutPipe()")
		return
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		dataflow <- makeCommandFailed(req, err, "StderrPipe()")
		return
	}

	go outputSender(tunnel.ChannelDirection_STDOUT, agg, stdout)
	go outputSender(tunnel.ChannelDirection_STDERR, agg, stderr)

	err = cmd.Start()
	if err != nil {
		// this path will occur if the command can't be found.  Other errors
		// are possible, but this is most likely.
		dataflow <- makeCommandFailed(req, err, "Start()")
		return
	}

	activeCount := 2
	for msg := range agg {
		if msg.closed {
			log.Printf("Channel %d closed", msg.channel)
			dataflow <- makeCommandDataClosed(req, msg.channel)
			activeCount--
			if activeCount == 0 {
				break
			}
		} else {
			dataflow <- makeCommandData(req, msg.channel, msg.value)
		}
	}

	log.Printf("Command closed both stdin and stdout.")

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			log.Printf("exited with code != 0")
			// The program has exited with an exit code != 0
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				log.Printf("Captured exit code %d", status.ExitStatus())
				dataflow <- makeCommandTermination(req, status.ExitStatus())
				return
			}
			log.Printf("Could not retrieve exit code.")
		} else {
			dataflow <- makeCommandFailed(req, err, "Wait()")
			return
		}
	}

	log.Printf("Exit code 0")
	dataflow <- makeCommandTermination(req, 0)
}
