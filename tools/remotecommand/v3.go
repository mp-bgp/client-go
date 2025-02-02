/*
Copyright 2016 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package remotecommand

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"

	"k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
)

// streamProtocolV3 implements version 3 of the streaming protocol for attach
// and exec. This version adds support for resizing the container's terminal.
type streamProtocolV3 struct {
	*streamProtocolV2

	resizeStream io.Writer
}

var _ streamProtocolHandler = &streamProtocolV3{}

func newStreamProtocolV3(options StreamOptions) streamProtocolHandler {
	CLog.Debugf("Initializing protocol V3")
	return &streamProtocolV3{
		streamProtocolV2: newStreamProtocolV2(options).(*streamProtocolV2),
	}
}

func (p *streamProtocolV3) createStreams(conn streamCreator) error {
	// set up the streams from v2
	if err := p.streamProtocolV2.createStreams(conn); err != nil {
		return err
	}

	// set up resize stream
	CLog.Tracef("p.Tty: %s", p.Tty)
	if p.Tty {
		headers := http.Header{}
		headers.Set(v1.StreamType, v1.StreamTypeResize)
		var err error
		CLog.Debugf("Creating resizeStream")
		CLog.Tracef("headers: %s", headers)
		p.resizeStream, err = conn.CreateStream(headers)
		if err != nil {
			CLog.Errorf("Error while creating streams: %s", err.Error())
			return err
		}
	}

	return nil
}

func (p *streamProtocolV3) handleResizes() {
	CLog.Debugf("someone called handleResise()")
	CLog.Tracef("p.resizeStream: %s", p.resizeStream)
	CLog.Tracef("p.TerminalSizeQueue: %s", p.TerminalSizeQueue)
	if p.resizeStream == nil || p.TerminalSizeQueue == nil {
		return
	}
	go func() {
		defer runtime.HandleCrash()

		encoder := json.NewEncoder(p.resizeStream)
		for {
			CLog.Debugf("Calling Next() on terminalsizequeue.")
			size := p.TerminalSizeQueue.Next()
			if size == nil {
				CLog.Errorf("Got empty size.")
				return
			}
			if err := encoder.Encode(&size); err != nil {
				CLog.Errorf("Unable to encode size.")
				runtime.HandleError(err)
			}
		}
	}()
}

func (p *streamProtocolV3) stream(conn streamCreator) error {
	CLog.Debugf("Create Streams")
	if err := p.createStreams(conn); err != nil {
		return err
	}

	// now that all the streams have been created, proceed with reading & copying

	errorChan := watchErrorStream(p.errorStream, &errorDecoderV3{})

	CLog.Debugf("Calling handleResizes()")
	p.handleResizes()

	p.copyStdin()

	var wg sync.WaitGroup
	p.copyStdout(&wg)
	p.copyStderr(&wg)

	// we're waiting for stdout/stderr to finish copying
	wg.Wait()

	// waits for errorStream to finish reading with an error or nil
	return <-errorChan
}

type errorDecoderV3 struct {
	errorDecoderV2
}
