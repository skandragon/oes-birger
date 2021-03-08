package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"syscall"

	"github.com/opsmx/grpc-bidir/pkg/tunnel"
	"golang.org/x/net/context"
)

func outputSender(channel tunnel.CommandData_Channel, c chan *outputMessage, in io.Reader) {
	buffer := make([]byte, 10240)
	for {
		n, err := in.Read(buffer)
		if n > 0 {
			c <- &outputMessage{channel: channel, value: buffer[:n], closed: false}
		}
		if err == io.EOF {
			c <- &outputMessage{channel: channel, value: emptyBytes, closed: true}
		}
		if err != nil {
			log.Printf("Got %v in read", err)
			c <- &outputMessage{channel: channel, value: emptyBytes, closed: true}
		}
	}
}

const (
	cmdStdin  = 0
	cmdStdout = 1
	cmdStderr = 2
)

type outputMessage struct {
	channel tunnel.CommandData_Channel
	value   []byte
	closed  bool
}

func makeCommandFailed(req *tunnel.CommandRequest, err error, message string) *tunnel.ASEventWrapper {
	var msg string
	if err != nil {
		msg = fmt.Sprintf("%s: %v", message, err)
	} else {
		msg = message
	}
	return &tunnel.ASEventWrapper{
		Event: &tunnel.ASEventWrapper_CommandTermination{
			CommandTermination: &tunnel.CommandTermination{
				Id:       req.Id,
				Target:   req.Target,
				ExitCode: 127,
				Message:  msg,
			},
		},
	}
}

func makeCommandTermination(req *tunnel.CommandRequest, exitstatus int) *tunnel.ASEventWrapper {
	return &tunnel.ASEventWrapper{
		Event: &tunnel.ASEventWrapper_CommandTermination{
			CommandTermination: &tunnel.CommandTermination{
				Id:       req.Id,
				Target:   req.Target,
				ExitCode: int32(exitstatus),
			},
		},
	}
}

func makeCommandData(req *tunnel.CommandRequest, channel tunnel.CommandData_Channel, data []byte) *tunnel.ASEventWrapper {
	return &tunnel.ASEventWrapper{
		Event: &tunnel.ASEventWrapper_CommandData{
			CommandData: &tunnel.CommandData{
				Id:      req.Id,
				Target:  req.Target,
				Body:    data,
				Channel: channel,
			},
		},
	}
}

func makeCommandDataClosed(req *tunnel.CommandRequest, channel tunnel.CommandData_Channel) *tunnel.ASEventWrapper {
	return &tunnel.ASEventWrapper{
		Event: &tunnel.ASEventWrapper_CommandData{
			CommandData: &tunnel.CommandData{
				Id:      req.Id,
				Target:  req.Target,
				Channel: channel,
				Closed:  true,
			},
		},
	}
}

func runCommand(dataflow chan *tunnel.ASEventWrapper, req *tunnel.CommandRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	registerCancelFunction(req.Id, cancel)
	defer unregisterCancelFunction(req.Id)

	// aggregation channel, for stdout and stderr to be send through.
	agg := make(chan *outputMessage)

	cmd := exec.CommandContext(ctx, os.Args[1], os.Args[2:]...)

	cmd.Env = req.Environment

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

	go outputSender(cmdStdout, agg, stdout)
	go outputSender(cmdStderr, agg, stderr)

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

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			// The program has exited with an exit code != 0
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				dataflow <- makeCommandTermination(req, status.ExitStatus())
				return
			}
		} else {
			dataflow <- makeCommandFailed(req, err, "Wait()")
			return
		}
	}

	dataflow <- makeCommandTermination(req, 0)
}
