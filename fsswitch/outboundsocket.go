/*
go-switch is released under the MIT License <http://www.opensource.org/licenses/mit-license.php
Copyright (C) Temlio Inc. All Rights Reserved.

Provides FreeSWITCH socket communication.

*/
package fsswitch

import (
	"errors"

	"log"
	"net"
)

var errFilterFail = errors.New("Event filter failed")

type OutboundSocket struct {
	Channel *Event
	*EventSocket
	conn net.Conn
}

func (self *OutboundSocket) Connect(eventHandlers map[string][]func(*Event), isEventJson bool) error {
	var err error
	var ev *Event
	log.Printf("Remote Address:%s", self.conn.RemoteAddr())
	self.EventSocket = NewEventSocket(self.conn, eventHandlers)
	go self.readLoop()
	self.Channel, err = self.ChannelConnect()
	if err != nil {
		return err
	}
	handledEvs := make([]string, len(eventHandlers))
	j := 0
	for k := range eventHandlers {
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

	if isEventJson {
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

	//self.Start()
	return nil
}

// Reads events from socket
func (self *OutboundSocket) Start() {
	for {
		ev, err := self.readEvent()
		if err != nil {
			log.Println("Error occured processing outbound start")
			self.Disconnect()
		}
		go self.dispatchEvent(ev)
	}

}
func NewOutboundSocket(conn net.Conn) *OutboundSocket {
	log.Println("Enter NewOutboundSocket ")
	outboundSocket := new(OutboundSocket)
	outboundSocket.conn = conn
	return outboundSocket
}

// HandleFunc is the function called on new incoming connections.
type HandleFunc func(*OutboundSocket)

func OutboundServer(addr string, fn HandleFunc) error {
	srv, err := net.Listen("tcp", addr)
	if err != nil {

		return err
	}
	for {
		c, err := srv.Accept()
		if err != nil {
			return err
		}
		log.Println("Enter OutboundServer")
		outboundFS := NewOutboundSocket(c)
		go fn(outboundFS)
	}
}
