/*
go-switch is released under the MIT License <http://www.opensource.org/licenses/mit-license.php
Copyright (C) Temlio Inc. All Rights Reserved.

Provides FreeSWITCH socket communication.

*/
package fsswitch

import (
	"errors"
	"net"
	"time"
)

var errMissingAuthRequest = errors.New("Missing auth request")
var errInvalidPassword = errors.New("Invalid password")
var errInvalidCommand = errors.New("Invalid command contains \\r or \\n")
var errFilterFailed = errors.New("Event filter failed")

type InboundSocket struct {
	fsaddress, fspassword string
	reconnects            int
	eventHandlers         map[string][]func(*Event)
	*EventSocket
	isEventJson bool
}

//Dial(fsaddress, fspassword string, reconnects uint8, eventHandlers map[string][]func(*Event)) (*EventSocket, error) {
func (self *InboundSocket) connect() error {

	var err error
	for i := 0; i < self.reconnects; i++ {
		c, err := net.Dial("tcp", self.fsaddress)
		if err == nil {
			self.EventSocket = NewEventSocket(c, self.eventHandlers)
			go self.readLoop()
			var ev *Event

			select {
			case err = <-self.err:
				return err
			case ev = <-self.auth:
				if ev.GetContentType() != "auth/request" {
					c.Close()
					return errMissingAuthRequest
				}

			}
			ev, err = self.Auth(self.fspassword)
			if err != nil {
				c.Close()
				return errInvalidPassword
			}

			handledEvs := make([]string, len(self.eventHandlers))
			j := 0
			for k := range self.eventHandlers {
				handledEvs[j] = k
				j++
			}

			var eventsCmd string
			for _, ev := range handledEvs {
				if ev == "ALL" {
					eventsCmd = "ALL"
					break
				}
				eventsCmd += " " + ev
			}
			eventsCmd += "\n\n"
			if self.isEventJson {
				ev, err = self.EventJson(eventsCmd)
				if err != nil && !ev.IsReplyTextSuccess() {
					return errFilterFailed
				}
			} else {
				ev, err = self.EventPlain(eventsCmd)
				if err != nil && !ev.IsReplyTextSuccess() {
					return errFilterFailed
				}
			}

			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return err
}

// Reads events from socket
func (self *InboundSocket) Start() {
	for {
		ev, err := self.readEvent()
		if err != nil {
			//if self.logger != nil {
			//	self.logger.Warning("<FSock> FreeSWITCH connection broken: attempting reconnect")
			//}
			connErr := self.connect()
			if connErr != nil {
				//return
				continue
			}
			time.Sleep(2 * time.Second)

			continue // Connection reset
		}
		go self.dispatchEvent(ev)
	}
	return
}
func NewInboundSocket(address string, password string, reconnects int, isEventJson bool, eventHandlers map[string][]func(*Event)) (*InboundSocket, error) {
	inboundSocket := InboundSocket{fsaddress: address, fspassword: password, reconnects: reconnects, isEventJson: isEventJson, eventHandlers: eventHandlers}
	err := inboundSocket.connect()
	if err != nil {
		return nil, err
	}
	return &inboundSocket, nil
}
